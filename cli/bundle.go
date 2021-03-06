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
	"github.com/qadium/plumber/shell"
	"io/ioutil"
	"log"
	"os"
	"path"
	"text/template"
)

type templateContext struct {
	Wrapper string
	Plumber *Bundle
}

// A Dockerfile template for python bundles. We'll make sure this is
// a standalone file in the future, so we can add different language
// types.
const dockerfileTemplate = `
FROM python:2.7.10-slim

RUN mkdir -p /usr/src/bundle
WORKDIR /usr/src/bundle

COPY . /usr/src/bundle
{{ if .Plumber.Install }}
{{ range .Plumber.Install }}
RUN {{ . }}
{{ end }}
{{ else }}
RUN pip install -r requirements.txt
{{ end }}
RUN pip install bottle
EXPOSE 9800
{{ range .Plumber.Env }}
ENV {{ . }}
{{ end }}
CMD ["python", "{{ .Wrapper }}"]
`

const wrapperTemplate = `
import {{ .Plumber.Name }}
from bottle import post, route, run, request, HTTPResponse

__INFO = {'bundle': '{{ .Plumber.Name }}',
  'inputs': {
    {{ range .Plumber.Inputs }}'{{ .Name }}': '{{ .Description }}',{{ end }}
  },
  'outputs': {
    {{ range .Plumber.Outputs }}'{{ .Name }}': '{{ .Description }}',{{ end }}
  }
}

@route('/info')
def info():
    return __INFO

@post('/')
def index():
    data = request.json

    # validate input data
    if data is None:
        raise HTTPResponse(
            body = "No JSON data received; did you forget to set 'Content-Type: application/json'?",
            status = 400
        )

	# Instead of throwing errors when required fields are missing,
	# maybe we just return the payload instead and add a note?
    {{ range .Plumber.Inputs }}
    if not '{{ .Name }}' in data:
        raise HTTPResponse(
            body = "Missing required '{{ .Name }}' field in JSON data.",
            status = 400
        )
    {{ end }}
    # run the enhancer
    output = {{ .Plumber.Name }}.run(data)

    # validate output data
    {{ range .Plumber.Outputs }}
    if not '{{ .Name }}' in output:
        raise HTTPResponse(
            body = "Unexpected output; missing '{{ .Name }}' field.",
            status = 501
        )
    {{ end }}

	# Instead of assuming the output contains a superset of the input
	# fields, we can add the input fields to the output here? It's a
	# performance hit, but it means the output is *always* a superset
	# of the input.

    return output

run(host='0.0.0.0', port=9800)
`

func removeTempFile(f *os.File) {
	filename := f.Name()
	log.Printf(" |  Removing '%s'", filename)
	err := os.RemoveAll(filename)
	if err != nil {
		log.Printf("    %v", err)
	} else {
		log.Printf("    Removed.")
	}
}

// Bundle stuff...
// BUG(echu): need to figure out how to handle conflicts in bundle names
func (ctx *Context) Bundle(bundlePath string) error {
	log.Printf("==> Creating bundle from '%s'", bundlePath)
	defer log.Printf("<== Bundling complete.")

	log.Printf(" |  Parsing bundle config.")
	bundleConfig, err := ParseBundleFromDir(bundlePath)
	if err != nil {
		return err
	}
	log.Printf("    %v", bundleConfig)

	log.Printf(" |  Making temp file for python wrapper")
	wrapper, err := ioutil.TempFile(bundlePath, "plumber")
	defer removeTempFile(wrapper)
	if err != nil {
		return err
	}
	log.Printf("    Created '%s'", wrapper.Name())

	templateCtx := templateContext{
		Wrapper: path.Base(wrapper.Name()),
		Plumber: bundleConfig,
	}

	log.Printf(" |  Writing wrapper.")
	tmpl, err := template.New("wrapper").Parse(wrapperTemplate)
	if err != nil {
		return err
	}
	if err := tmpl.Execute(wrapper, templateCtx); err != nil {
		return err
	}
	log.Printf("    Done.")

	log.Printf(" |  Making temp file for Dockerfile")
	dockerfile, err := ioutil.TempFile(bundlePath, "plumber")
	defer removeTempFile(dockerfile)
	if err != nil {
		return err
	}
	log.Printf("    Created '%s'", dockerfile.Name())

	log.Printf(" |  Writing Dockerfile.")
	tmpl, err = template.New("dockerfile").Parse(dockerfileTemplate)
	if err != nil {
		return err
	}
	if err := tmpl.Execute(dockerfile, templateCtx); err != nil {
		return err
	}
	log.Printf("    Done.")

	log.Printf(" |  Building container.")
	err = shell.RunAndLog(ctx.DockerCmd, "build", "--pull", "-t", ctx.GetImage(bundleConfig.Name), "-f", dockerfile.Name(), bundlePath)
	if err != nil {
		return err
	}
	log.Printf("    Container '%s' built.", ctx.GetImage(bundleConfig.Name))
	return nil
}
