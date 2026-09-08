package main

import (
	"fmt"
	"os"

	"github.com/aruzen/ariadne/internal/frontend/cui"
)

func main() {
	if err := cui.Run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "ariadne: %v\n", err)
		os.Exit(1)
	}
}
