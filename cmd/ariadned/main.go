package main

import (
	"log"
	"os"

	daemonapp "github.com/aruzen/ariadne/internal/app/daemon"
)

func main() {
	if err := daemonapp.Run(os.Args[1:]); err != nil {
		log.Printf("ariadned: %v", err)
		os.Exit(1)
	}
}
