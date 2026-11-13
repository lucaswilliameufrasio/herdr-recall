package main

import (
	"fmt"
	"os"
)

func main() {
	args := []string{}
	if len(os.Args) > 1 {
		args = os.Args[1:]
	}
	path, pathErr := defaultLatestPath()
	if len(args) > 0 && args[0] == "latest" {
		if pathErr != nil {
			fmt.Fprintln(os.Stderr, "Não foi possível mostrar as capturas.")
			os.Exit(1)
		}
		if err := run(args, os.Stdout, os.Stderr, path); err != nil {
			fmt.Fprintln(os.Stderr, "Não foi possível mostrar as capturas.")
			os.Exit(1)
		}
		return
	}
	if err := run(args, os.Stdout, os.Stderr, path); err != nil {
		fmt.Fprintln(os.Stderr, "Não foi possível mostrar as capturas.")
		os.Exit(1)
	}
}
