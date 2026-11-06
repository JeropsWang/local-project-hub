//go:build windows && desktop

package main

import (
	"log"

	"github.com/JeropsWang/local-project-hub/internal/bootstrap"
)

func main() {
	if err := bootstrap.Run(frontendAssets()); err != nil {
		log.Fatal(err)
	}
}
