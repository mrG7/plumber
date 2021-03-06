/**
 * Copyright 2015 Qadium, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */
package cli

import (
	"fmt"
	"log"
	//	"gopkg.in/yaml.v2"
	"os"
	//	"text/template"
	"github.com/qadium/plumber/bindata"
	"github.com/qadium/plumber/graph"
	"github.com/qadium/plumber/shell"
	"os/exec"
	"path/filepath"
	"text/template"
	// "golang.org/x/oauth2/google"
	// "golang.org/x/oauth2"
	// "google.golang.org/cloud"
	// "google.golang.org/cloud/container"
	// kubectl "github.com/GoogleCloudPlatform/kubernetes/pkg/kubectl/cmd"
	// cmdutil "github.com/GoogleCloudPlatform/kubernetes/pkg/kubectl/cmd/util"
	// "github.com/GoogleCloudPlatform/kubernetes/pkg/client/clientcmd"
	// "os"
)

type pipelineInfo struct {
	path   string
	name   string
	commit string
}

type kubeData struct {
	BundleName     string
	ExternalFacing bool
	PipelineName   string
	PipelineCommit string
	PlumberVersion string
	PlumberCommit  string
	ImageName      string
	Args           []string
}

func bundlesToGraphs(bundles []*Bundle) []*graph.Node {
	nodes := make([]*graph.Node, len(bundles))
	// this map maps inputs to the index of the node that uses it
	m := make(map[string]int)

	// build a map to create the DAG
	for i, bundle := range bundles {
		nodes[i] = graph.NewNode(bundle.Name)
		for _, input := range bundle.Inputs {
			m[input.Name] = i
		}
	}

	for i, bundle := range bundles {
		for _, output := range bundle.Outputs {
			if v, ok := m[output.Name]; ok {
				nodes[i].AddChildren(nodes[v])
			}
		}
	}
	return nodes
}

func localStart(ctx *Context, sortedPipeline []string) error {
	log.Printf(" |  Starting bundles...")
	managerDockerArgs := []string{"run", "-p", "9800:9800", "--rm", ctx.GetManagerImage()}
	// walk through the reverse sorted bundles and start them up
	for i := len(sortedPipeline) - 1; i >= 0; i-- {
		bundleName := sortedPipeline[i]
		log.Printf("    Starting: '%s'", bundleName)
		cmd := exec.Command(ctx.DockerCmd, "run", "-d", "-P", ctx.GetImage(bundleName))
		containerId, err := cmd.Output()
		if err != nil {
			return err
		}

		defer func() {
			log.Printf("    Stopping: '%s'", bundleName)
			cmd := exec.Command(ctx.DockerCmd, "rm", "-f", string(containerId)[0:4])
			_, err := cmd.Output()
			if err != nil {
				panic(err)
			}
			log.Printf("    Stopped.")
		}()

		log.Printf("    Started: %s", string(containerId))
		cmd = exec.Command(ctx.DockerCmd, "inspect", "--format='{{(index (index .NetworkSettings.Ports \"9800/tcp\") 0).HostPort}}'", string(containerId)[0:4])
		portNum, err := cmd.Output()
		if err != nil {
			return err
		}
		// get the docker host IP for local deploy
		hostIp, err := ctx.GetDockerHost()
		if err != nil {
			return err
		}
		managerDockerArgs = append(managerDockerArgs, fmt.Sprintf("http://%s:%s", hostIp, string(portNum[:len(portNum)-1])))
	}
	log.Printf("    Done.")
	log.Printf("    Args passed to 'docker': %v", managerDockerArgs)

	log.Printf(" |  Running manager. CTRL-C to quit.")
	err := shell.RunAndLog(ctx.DockerCmd, managerDockerArgs...)
	if err != nil {
		return err
	}
	log.Printf("    Done.")
	return nil
}

func writeKubernetesTemplate(tmplType string, destFilename string, templateData kubeData) error {
	tmpl, err := bindata.Asset(fmt.Sprintf("templates/%s.yaml", tmplType))
	if err != nil {
		return err
	}

	file, err := os.Create(destFilename)
	if err != nil {
		return err
	}
	defer func() {
		if err := file.Close(); err != nil {
			panic(err)
		}
	}()

	tmplFile, err := template.New("template").Parse(string(tmpl))
	if err != nil {
		return err
	}
	if err := tmplFile.Execute(file, templateData); err != nil {
		return err
	}
	return nil
}

func writeKubernetesFiles(ctx *Context, templateData kubeData) error {
	log.Printf(" |     Writing '%s'", templateData.BundleName)
	k8s := ctx.KubernetesPath(templateData.PipelineName)

	log.Printf("       Creating service file.")
	err := writeKubernetesTemplate("service", fmt.Sprintf("%s/%s.yaml", k8s, templateData.BundleName), templateData)
	if err != nil {
		return err
	}
	log.Printf("       Created.")

	log.Printf("       Creating replication controller file.")
	err = writeKubernetesTemplate("replication-controller", fmt.Sprintf("%s/%s-rc.yaml", k8s, templateData.BundleName), templateData)
	if err != nil {
		return err
	}
	log.Printf("       Created.")
	log.Printf("       Done.")
	return nil
}

func remoteStart(ctx *Context, sortedPipeline []string, projectId string, pipeline pipelineInfo) error {
	// we can probably get the project name with google cloud SDK
	log.Printf("   Creating '%s' directory...", ctx.KubeSubdir)
	k8s := ctx.KubernetesPath(pipeline.name)

	if err := os.MkdirAll(k8s, 0755); err != nil {
		return err
	}
	log.Printf("   Created.")

	args := []string{}

	for i := len(sortedPipeline) - 1; i >= 0; i-- {
		bundleName := sortedPipeline[i]
		localDockerTag := ctx.GetImage(bundleName)
		remoteDockerTag := fmt.Sprintf("gcr.io/%s/plumber-%s", projectId, bundleName)
		data := kubeData{
			BundleName:     bundleName,
			ImageName:      remoteDockerTag,
			PlumberVersion: ctx.Version,
			PlumberCommit:  ctx.GitCommit,
			PipelineName:   pipeline.name,
			PipelineCommit: pipeline.commit,
			ExternalFacing: false,
			Args:           []string{},
		}

		// step 1. re-tag local containers to gcr.io/$GCE/$pipeline-$bundlename
		log.Printf("    Retagging: '%s'", bundleName)
		err := shell.RunAndLog(ctx.DockerCmd, "tag", "-f", localDockerTag, remoteDockerTag)
		if err != nil {
			return err
		}
		// step 2. push them to gce
		log.Printf("    Submitting: '%s'", remoteDockerTag)
		err = shell.RunAndLog(ctx.GcloudCmd, "docker", "push", remoteDockerTag)
		if err != nil {
			return err
		}
		// step 3. generate k8s files in pipelinePath
		if err := writeKubernetesFiles(ctx, data); err != nil {
			return err
		}

		// append to arglist (args now in sorted order)
		args = append(args, fmt.Sprintf("http://%s:9800", bundleName))
	}
	// create the manager service
	data := kubeData{
		BundleName:     "manager",
		ImageName:      fmt.Sprintf("gcr.io/%s/plumber-manager", projectId),
		PlumberVersion: ctx.Version,
		PlumberCommit:  ctx.GitCommit,
		PipelineName:   pipeline.name,
		PipelineCommit: pipeline.commit,
		ExternalFacing: true,
		Args:           args,
	}
	// step 1. re-tag local containers to gcr.io/$GCE/$pipeline-$bundlename
	log.Printf("    Retagging: '%s'", ctx.GetManagerImage())
	err := shell.RunAndLog(ctx.DockerCmd, "tag", "-f", ctx.GetManagerImage(), data.ImageName)
	if err != nil {
		return err
	}
	// step 2. push them to gce
	log.Printf("    Submitting: '%s'", data.ImageName)
	err = shell.RunAndLog(ctx.GcloudCmd, "docker", "push", data.ImageName)
	if err != nil {
		return err
	}
	// step 3. generate k8s file in pipeline
	if err := writeKubernetesFiles(ctx, data); err != nil {
		return err
	}

	// step 4. launch all the services
	err = shell.RunAndLog(ctx.KubectlCmd, "create", "-f", k8s)
	if err != nil {
		return err
	}
	// step 5: open up the firewall?
	return nil
}

func (ctx *Context) Start(pipeline, gce string) error {
	log.Printf("==> Starting '%s' pipeline", pipeline)
	defer log.Printf("<== '%s' finished.", pipeline)

	log.Printf(" |  Building dependency graph.")
	path, err := ctx.GetPipeline(pipeline)
	if err != nil {
		return err
	}

	configs, err := filepath.Glob(fmt.Sprintf("%s/*.yml", path))
	if err != nil {
		return err
	}

	ctxs := make([]*Bundle, len(configs))
	for i, config := range configs {
		ctxs[i], err = ParseBundle(config)
		if err != nil {
			return err
		}
	}

	g := bundlesToGraphs(ctxs)
	sortedPipeline, err := graph.ReverseTopoSort(g)
	if err != nil {
		return err
	}
	log.Printf("    Reverse sorted: %v", sortedPipeline)
	log.Printf("    Completed.")

	if gce != "" {
		// start GOOGLE experiments?
		// when start is invoked with --gce PROJECT_ID, this piece of code
		// should be run
		// client, err := google.DefaultClient(oauth2.NoContext, "https://www.googleapis.com/auth/compute")
		// if err != nil {
		// 	return err
		// }
		// cloudCtx := cloud.NewContext("kubernetes-fun", client)
		//
		// resources, err := container.Clusters(cloudCtx, "")
		// if err != nil {
		// 	return err
		// }
		// for _, op := range resources {
		// 	log.Printf("%v", op)
		// }
		//
		// loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
		// log.Printf("loading rules: %v", *loadingRules)
		// configOverrides := &clientcmd.ConfigOverrides{}
		// kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
		// cfg, err := kubeConfig.ClientConfig()
		// if err != nil {
		// 	return err
		// }

		// well, we just shell out!
		// f := cmdutil.NewFactory(nil)
		// cmd := kubectl.NewCmdCreate(f, os.Stdout)
		// f.BindFlags(cmd.PersistentFlags())
		// cmd.Flags().Set("filename", "/Users/echu/.plumber/foo/k8s")
		// cmd.Run(cmd, []string{})

		// end GOOGLE experiments

		info := pipelineInfo{
			name:   pipeline,
			path:   path,
			commit: "",
		}
		log.Printf(" |  Running remote pipeline.")
		return remoteStart(ctx, sortedPipeline, gce, info)
	} else {
		log.Printf(" |  Running local pipeline.")
		return localStart(ctx, sortedPipeline)
	}
}
