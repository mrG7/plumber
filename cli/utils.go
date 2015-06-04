package cli

import (
	"fmt"
	"os/user"
	"os"
)

const plumberDir = ".plumber"

// Given the `name` of a pipeline, return the path where we should store
// information about it.
func PipelinePath(name string) (string, error) {
	usr, err := user.Current()
	if err != nil {
		return "", err
	}
	path := fmt.Sprintf("%s/%s/%s", usr.HomeDir, plumberDir, name)
	return path, nil
}

// Get a pipeline; this differs from PipelinePath in that it also checks
// if the file / path exists.
func GetPipeline(name string) (string, error) {
	path, err := PipelinePath(name)
	if err != nil {
		return "", err
	}
	// make sure file exists and we have permissions, etc.
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	return path, nil
}
