package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func main() {

	var rootCmd = &cobra.Command{
		Use:   "istioctl [mesh]",
		Short: "Istioctl is a tool for managinging multi-zone Istio meshes",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("Inside rootCmd Run with args: %v\n", args)
		},
	}
	rootCmd.Execute()
}
