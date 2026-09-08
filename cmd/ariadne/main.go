package main

import (
	"fmt"
	"os"

	"github.com/aruzen/ariadne/internal/app"
)

func main() {
	if err := app.Run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "ariadne: %v\n", err)
		os.Exit(1)
	}
}
