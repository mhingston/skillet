package main

import (
	"context"
	"fmt"
	"os"

	"github.com/mhingston/skillet/internal/knowledgecli"
)

func main() {
	if err := knowledgecli.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "skillet-knowledge:", err)
		os.Exit(2)
	}
}
