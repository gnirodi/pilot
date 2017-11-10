package main

import (
	"github.com/spf13/cobra"
	"istio.io/pilot/bridge/istioctl/cmd"
)

func main() {

	var rootCmd = &cobra.Command{
		Use:   "istioctl [view]",
		Short: "Istioctl is a tool for managinging multi-cluster Istio meshes",
	}
	rootCmd.AddCommand(cmd.NewViewCommand())
	rootCmd.Execute()
}
