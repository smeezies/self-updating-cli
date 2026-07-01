package main

import (
	"fmt"
	"github.com/smeezies/self-updating-cli/internal/version"
)

func main() {
	fmt.Printf("myapp %s\n", version.Version)
}