package main

import (
	"context"
	"os"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/cli"
)

func main() {
	os.Exit(cli.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
