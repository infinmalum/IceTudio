// Packaged-app launcher keeps writable application data outside a read-only
// installation. It passes all CLI arguments to the same desktop/IPC binary.
package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	executable, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	if _, present := os.LookupEnv("ICETUDIO_DATA_DIR"); !present {
		dir, err := os.UserConfigDir()
		if err != nil {
			log.Fatal(err)
		}
		if err = os.Setenv("ICETUDIO_DATA_DIR", filepath.Join(dir, "IceTudio")); err != nil {
			log.Fatal(err)
		}
	}
	command := exec.Command(filepath.Join(filepath.Dir(executable), "icetudio.exe"), os.Args[1:]...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		if status, ok := err.(*exec.ExitError); ok {
			os.Exit(status.ExitCode())
		}
		log.Fatal(err)
	}
}
