package main

import (
	"os"

	"github.com/mhingston/skillet/internal/clientcli"
)

func main() { clientcli.Main(os.Args[1:]) }
