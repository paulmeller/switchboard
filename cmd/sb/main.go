package main

import (
	"os"

	"switchboard/internal/cmd"
)

func main() { os.Exit(cmd.Main(os.Args[1:])) }
