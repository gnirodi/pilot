package cmd

import (
	"flag"
	"fmt"
	"github.com/spf13/cobra"
	"os"
	"path/filepath"

	meshv1 "istio.io/pilot/bridge/clientset/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1"

	// "k8s.io/client-go/kubernetes"
	// "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	// clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	// Uncomment the following line to load the gcp plugin (only required to authenticate against GKE clusters).
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
)

var viewCmd = &cobra.Command{
	Use:   "view [mesh]",
	Short: "Displays various views of Istio control plane components",
}

var viewMeshCmd = &cobra.Command{
	Use:   "mesh []",
	Short: "Lists clusters from configured cluster registries and displays their mesh status",
	Run:   ViewMesh,
}

func NewViewCommand() *cobra.Command {
	viewCmd.AddCommand(newViewMeshCommand())
	return viewCmd
}

func newViewMeshCommand() *cobra.Command {
	return viewMeshCmd
}

func ViewMesh(cmd *cobra.Command, args []string) {
	var kubeconfig *string
	if home := homeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()

	pathOptions := clientcmd.NewDefaultPathOptions()
	startingConfig, err := pathOptions.GetStartingConfig()
	if err != nil {
		panic(err)
	}
	for ctxName, ctx := range startingConfig.Contexts {
		fmt.Printf("Cluster: %s\n", ctx.Cluster)
		startingConfig.CurrentContext = ctxName
		clientcmd.ModifyConfig(pathOptions, *startingConfig, true)

		config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
		if err != nil {
			panic(err.Error())
		}
		client, err := meshv1.NewForConfig(config)
		if err != nil {
			fmt.Printf("Could not access cluster %s. Error: %s\n", ctx.Cluster, err)
		} else {
			meshList, err := client.PkgV1().Meshs().List(v1.ListOptions{})
			if err != nil {
				fmt.Printf("Cluster: %s errored out retrieving mesh information: %s\n", ctx.Cluster, err.Error())
				continue
			}
			fmt.Printf("%v", meshList)
		}
		fmt.Println("------------------------------------------")
	}
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // windows
}
