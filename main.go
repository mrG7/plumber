package main

import (
	"errors"
	"fmt"
	"github.com/codegangsta/cli"
	plumb "github.com/qadium/plumb/cli"
	"os"
)

func createRequiredArgCheck(check func(args cli.Args) bool, message string) func(c *cli.Context) error {
	return func(c *cli.Context) error {
		if !check(c.Args()) {
			fmt.Println(message)
			return errors.New(message)
		}
		return nil
	}
}

func exactly(num int) func(args cli.Args) bool {
	return func(args cli.Args) bool {
		return len(args) == num
	}
}

func atLeast(num int) func(args cli.Args) bool {
	return func(args cli.Args) bool {
		return len(args) >= num
	}
}

func main() {
	versionString := versionString()
	app := cli.NewApp()
	app.Name = "plumb"
	app.Usage = "a command line tool for managing distributed data pipelines"
	app.Version = versionString
	// app.Flags = []cli.Flag{
	// 	cli.StringFlag{
	// 		Name:   "server, s",
	// 		Value:  "/var/run/plumb.sock",
	// 		Usage:  "location of plumb server socket",
	// 		EnvVar: "LINK_SERVER",
	// 	},
	// }
	app.Commands = []cli.Command{
		{
			Name:   "add",
			Usage:  "add a plumb-enabled bundle to a pipeline",
			Before: createRequiredArgCheck(atLeast(2), "Please provide both a pipeline name and a bundle path."),
			Action: func(c *cli.Context) {
				pipeline := c.Args()[0]
				bundles := c.Args()[1:]
				if err := plumb.Add(pipeline, bundles...); err != nil {
					panic(err)
				}
			},
		},
		{
			Name:   "create",
			Usage:  "create a pipeline managed by plumb",
			Before: createRequiredArgCheck(exactly(1), "Please provide a pipeline name."),
			Action: func(c *cli.Context) {
				path := c.Args().First()
				if err := plumb.Create(path); err != nil {
					panic(err)
				}
			},
		},
		{
			Name:	"bootstrap",
			Usage:  "bootstrap local setup for use with plumb",
			Description:
`The bootstrap command builds the latest manager for use with plumb.
This packages the manager into a minimal container for use on localhost.

When running the pipeline on Google Cloud, the manager container is
pushed to your project's private repository.`,
			Action: func(c *cli.Context) {
				if err := plumb.Bootstrap(GitCommit); err != nil {
					panic(err)
				}
			},
		},
		{
			Name:   "start",
			Usage:  "start a pipeline managed by plumb",
			Before: createRequiredArgCheck(exactly(1), "Please provide a pipeline name."),
			Action: func(c *cli.Context) {
				pipeline := c.Args().First()
				if err := plumb.Start(pipeline); err != nil {
					panic(err)
				}
			},
		},
		{
			Name:   "bundle",
			Usage:  "bundle a node for use in a pipeline managed by plumb",
			Before: createRequiredArgCheck(exactly(1), "Please provide a bundle path."),
			Action: func(c *cli.Context) {
				path := c.Args().First()
				if err := plumb.Bundle(path); err != nil {
					panic(err)
				}
			},
		},
		{
			Name:  "version",
			Usage: "more detailed version information for plumb",
			Action: func(c *cli.Context) {
				commit := GitCommit
				if GitDirty != "" {
					commit += "+CHANGES"
				}
				fmt.Println("plumb version:", versionString)
				fmt.Println("git commit:", commit)
			},
		},
	}
	app.Run(os.Args)
}

func versionString() string {
	versionString := Version
	if VersionPrerelease != "" {
		versionString += "-" + VersionPrerelease
	}
	return versionString
}
