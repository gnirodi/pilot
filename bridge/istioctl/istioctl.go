package main

import (
	"flag"

	"github.com/spf13/cobra"
	"istio.io/pilot/bridge/istioctl/cmd"
)

func main() {

	var rootCmd = &cobra.Command{
		Use:   "istioctl [update-mesh]",
		Short: "Istioctl is a tool for managinging multi-cluster Istio meshes",
	}
	rootCmd.AddCommand(cmd.NewUpdateMeshCommand())
	flag.Parse()
	rootCmd.Execute()
}
