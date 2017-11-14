package cmd

import (
	"flag"
	"fmt"
	"github.com/spf13/cobra"
	"os"
	"path/filepath"
	"strings"

	crv1 "istio.io/pilot/bridge/api/pkg/v1"
	meshv1 "istio.io/pilot/bridge/clientset/v1"
	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	extv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
	// "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	// Uncomment the following line to load the gcp plugin (only required to authenticate against GKE clusters).
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
)

type MeshStatus string

const (
	MeshStatusUnknown       MeshStatus = "Unknown!!"
	MeshStatusNotMesh       MeshStatus = "Not a mesh"
	MeshStatusOutOfSync     MeshStatus = "Out of sync"
	MeshStatusMisconfigured MeshStatus = "Misconfigured"
	MeshStatusOK            MeshStatus = "Configured OK"
	MeshStatusUpdated       MeshStatus = "Updated"
	MeshStatusUpdateFailed  MeshStatus = "Update failed!!"
	MeshStatusLeftUntouched MeshStatus = "Left Untouched"
)

// A generic cluster registry interface. This could be a superset of various K8s and on-prem registries.
// Irrespective of the environment, the cluster is expected to support the Mesh client interface
type ClusterRegistry interface {

	// Expected to return a set of globally unique names that serve as references to clusters of that environment
	GetClusterContexts() map[string]bool

	// Obtains the client for creating a custom resource definition in a cluster given the cluster context
	GetCrdClientset(clusterContext string) (*extv1.Clientset, error)

	// Obtains the mesh client for a given cluster context
	GetMeshClientset(clusterContext string) (*meshv1.Clientset, error)
}

// Currently only supports KubectlConfigRegistry files. In the future should support mixed environments
type IstioClusterRegistry struct {
	registries       []*ClusterRegistry
	ctxToRegistryMap map[string]*ClusterRegistry
}

type IstioClusterRegistryError struct {
	errorMsg string
}

type UpdateMeshError struct {
	errorMsg string
}

// Registry that's intrincically part of Kube config
type KubectlConfigRegistry struct {
	kubeconfig     *string
	pathOptions    *clientcmd.PathOptions
	startingConfig *clientcmdapi.Config
	ctxConfigs     map[string]*rest.Config
	crdClientsets  map[string]*extv1.Clientset
	meshClientsets map[string]*meshv1.Clientset
}

var meshConfigFileName string
var clusters string
var services string

var updateMeshCmd = &cobra.Command{
	Use:   "update-mesh",
	Short: "Updates selected clusters and services to join the mesh",
	RunE:  UpdateMesh,
}

var istioClusterRegistry *IstioClusterRegistry

func NewUpdateMeshCommand() *cobra.Command {
	updateMeshCmd.PersistentFlags().StringVar(&meshConfigFileName, "mesh-config", "./mesh.yaml", "A configuration file containing a resource of the kind Mesh (meshs.config.istio.io)")
	updateMeshCmd.PersistentFlags().StringVar(&clusters, "clusters", "", "A comma separated list of clusters that ought to be updated for this mesh. The keyword 'all' will update all activated clusters.")
	updateMeshCmd.PersistentFlags().StringVar(&clusters, "services", "", "A comma separated list of services in the clusters that ought to be updated for the mesh. The keyword 'all' will update all services in the listed clusters")
	istioClusterRegistry = NewIstioClusterRegistry()
	kubectlConfigRegistry := NewKubectlConfigRegistry()
	istioClusterRegistry.AddClusterRegistry(&kubectlConfigRegistry)
	return updateMeshCmd
}

func UpdateMesh(cmd *cobra.Command, args []string) error {
	ctxToZSpecMap, zoneNameToCtxMap, meshConfig, err := GetMeshConfig(meshConfigFileName)
	if err != nil {
		return NewUpdateMeshError(fmt.Sprintf("Cannot continue. Cannot read Mesh Config from '%s'. Error: %s", meshConfigFileName, err.Error()))
	}
	if len(ctxToZSpecMap) == 0 {
		return NewUpdateMeshError(fmt.Sprintf("Cannot continue. Mesh Config '%s' is empty", meshConfigFileName))
	}
	zoneNames := map[string]bool{}
	tmpClusters := strings.Split(clusters, ",")
	for _, zoneName := range tmpClusters {
		if zoneName == "" {
			continue
		}
		zoneNames[zoneName] = true
	}
	// Check if the clusters have an entry in the mesh config
	for zoneName, _ := range zoneNames {
		_, found := zoneNameToCtxMap[zoneName]
		if !found {
			return NewUpdateMeshError(
				fmt.Sprintf("Cannot continue. Cluster '%s' specified in option --clusters does not have an entry in the mesh config '%s'", zoneName, meshConfigFileName))
		}
	}
	_, err = istioClusterRegistry.GetClusterContexts()
	if err != nil {
		return err
	}
	ctxToMeshStatus, anyWithUnknownSts, anyNeedUpdate := istioClusterRegistry.GetCurrentMeshStatus(ctxToZSpecMap)
	switch {
	case len(zoneNames) == 0:
		fmt.Println("No clusters provided via the --clusters option. Only current mesh status is being reported.")
		PrintMeshStatus(ctxToMeshStatus, ctxToZSpecMap)
		return nil
	case len(zoneNames) > 0 && anyWithUnknownSts:
		for zoneName, _ := range zoneNames {
			ctx, _ := zoneNameToCtxMap[zoneName]
			sts, found := ctxToMeshStatus[ctx]
			if !found || sts == MeshStatusUnknown {
				return NewUpdateMeshError(fmt.Sprintf("Cannot continue. One of the clusters provided in --clusters '%s' is not available.", clusters))
			}
		}
		break
	case !anyNeedUpdate:
		fmt.Printf("\n\nAll clusters provided in --clusters '%s' are up to date\n", clusters)
		PrintMeshStatus(ctxToMeshStatus, ctxToZSpecMap)
		return nil
	}
	fmt.Printf("\n\nUpdating clusters '%s' with the mesh spec....\n", clusters)
	for ctx, sts := range ctxToMeshStatus {
		if sts == MeshStatusMisconfigured || sts == MeshStatusNotMesh || sts == MeshStatusOutOfSync {
			zoneSpec, _ := ctxToZSpecMap[ctx]
			err = istioClusterRegistry.UpdateCluster(ctx, zoneSpec.ZoneName, sts, &meshConfig)
			if err != nil {
				ctxToMeshStatus[ctx] = MeshStatusUpdateFailed
				fmt.Printf("Cluster '%s' update failed. Error: %s", zoneSpec.ZoneName, err.Error())
				continue
			}
			ctxToMeshStatus[ctx] = MeshStatusUpdated
		}
	}
	PrintMeshStatus(ctxToMeshStatus, ctxToZSpecMap)
	return nil
}

func GetMeshConfig(fileName string) (clusterCtxToZSpecMap map[string]crv1.ZoneSpec, clusterNameToCtxMap map[string]string, meshResource crv1.Mesh, err error) {
	yamlRdr, err := os.Open(fileName)
	if err != nil {
		return clusterCtxToZSpecMap, clusterNameToCtxMap, meshResource, err
	}
	decoder := yaml.NewYAMLOrJSONDecoder(yamlRdr, 100)
	err = decoder.Decode(&meshResource)
	if err != nil {
		return clusterCtxToZSpecMap, clusterNameToCtxMap, meshResource, err
	}
	clusterCtxToZSpecMap = map[string]crv1.ZoneSpec{}
	clusterNameToCtxMap = map[string]string{}
	for _, zoneSpec := range meshResource.Spec.Zones {
		existingSpec, found := clusterCtxToZSpecMap[zoneSpec.Name]
		if found {
			panic(fmt.Sprintf("Mesh configs must have unique name. Duplicate contexts '%s' found for '%v' and '%v'", zoneSpec.Name, existingSpec, zoneSpec))
		}
		_, found = clusterCtxToZSpecMap[zoneSpec.ZoneName]
		if found {
			panic(fmt.Sprintf("Mesh configs must have unique cluster contexts. Duplicate contexts '%s' found for '%v' and '%v'", zoneSpec.ZoneName, existingSpec, zoneSpec))
		}
		clusterCtxToZSpecMap[zoneSpec.Name] = zoneSpec
		clusterNameToCtxMap[zoneSpec.ZoneName] = zoneSpec.Name
	}
	return clusterCtxToZSpecMap, clusterNameToCtxMap, meshResource, nil
}

func PrintMeshStatus(ctxToStsMap map[string]MeshStatus, clusterCtxToZSpecMap map[string]crv1.ZoneSpec) {
	lineFormat := "%-[1]*.[1]*[2]s    %-[3]*.[3]*[4]s    %s\n"
	colHeaderCluster := "Cluster Name"
	colHeaderCtx := "Cluster Context"
	colHeaderSts := "Status"
	maxZNameLen := len(colHeaderCluster)
	maxCtxLen := len(colHeaderCtx)
	for ctx, _ := range ctxToStsMap {
		ctxLen := len(ctx)
		zSpec, _ := clusterCtxToZSpecMap[ctx]
		zNameLen := len(zSpec.ZoneName)
		if ctxLen > maxCtxLen {
			maxCtxLen = ctxLen
		}
		if zNameLen > maxZNameLen {
			maxZNameLen = zNameLen
		}
	}
	colClusterBorder := strings.Repeat("-", maxZNameLen)
	colCtxBorder := strings.Repeat("-", maxCtxLen)
	colStsBorder := strings.Repeat("-", len(MeshStatusLeftUntouched))

	fmt.Println("")
	fmt.Printf(lineFormat, maxZNameLen, colHeaderCluster, maxCtxLen, colHeaderCtx, colHeaderSts)
	fmt.Printf(lineFormat, maxZNameLen, colClusterBorder, maxCtxLen, colCtxBorder, colStsBorder)
	for ctx, sts := range ctxToStsMap {
		zSpec, _ := clusterCtxToZSpecMap[ctx]
		fmt.Printf(lineFormat, maxZNameLen, zSpec.ZoneName, maxCtxLen, ctx, sts)
	}
}

func NewIstioClusterRegistry() *IstioClusterRegistry {
	return &IstioClusterRegistry{[]*ClusterRegistry{}, map[string]*ClusterRegistry{}}
}

// Delegates to GetClusterContexts of registries but additionally returns an error
func (r *IstioClusterRegistry) GetClusterContexts() (map[string]bool, error) {
	cr := map[string]bool{}
	for _, registry := range r.registries {
		regClusters := (*registry).GetClusterContexts()
		for ctx, _ := range regClusters {
			cr[ctx] = true
			prevRegistry, found := r.ctxToRegistryMap[ctx]
			if found {
				panic(fmt.Sprintf("Registry contexts must be unique. Duplicate context '%s' specified in the following registries '%v' and '%v'",
					ctx, *prevRegistry, *registry))
			}
			r.ctxToRegistryMap[ctx] = registry
		}
	}
	if len(cr) == 0 {
		return cr, NewIstioClusterRegistryError("No clusters were found in the configured registry")
	}
	return cr, nil
}

func (r *IstioClusterRegistry) AddClusterRegistry(cr *ClusterRegistry) {
	r.registries = append(r.registries, cr)
}

func (r *IstioClusterRegistry) GetMeshClientset(clusterContext string) (*meshv1.Clientset, error) {
	registry, found := r.ctxToRegistryMap[clusterContext]
	if !found {
		panic(fmt.Sprintf("Cluster context '%s' does not belong to this registry", clusterContext))
	}
	return (*registry).GetMeshClientset(clusterContext)
}

func (r *IstioClusterRegistry) GetCrdClientset(clusterContext string) (*extv1.Clientset, error) {
	registry, found := r.ctxToRegistryMap[clusterContext]
	if !found {
		panic(fmt.Sprintf("Cluster context '%s' does not belong to this registry", clusterContext))
	}
	return (*registry).GetCrdClientset(clusterContext)
}

func (r *IstioClusterRegistry) GetCurrentMeshStatus(ctxToZSpecMap map[string]crv1.ZoneSpec) (ctxToStsMap map[string]MeshStatus, hasUnknown, needsUpdate bool) {
	ctxToStsMap = map[string]MeshStatus{}
	hasUnknown = false
	needsUpdate = false
	for ctx, zoneSpec := range ctxToZSpecMap {
		if !zoneSpec.Activate {
			ctxToStsMap[ctx] = MeshStatusLeftUntouched
			continue
		}
		clientset, err := r.GetMeshClientset(ctx)
		if err != nil {
			// Cannot connect to the cluster
			ctxToStsMap[ctx] = MeshStatusUnknown
			hasUnknown = false
			continue
		}
		meshList, err := clientset.PkgV1().Meshs().List(v1.ListOptions{})
		switch {
		case err != nil:
			ctxToStsMap[ctx] = MeshStatusNotMesh
			needsUpdate = true
			break
		case len(meshList.Items) == 0:
			ctxToStsMap[ctx] = MeshStatusNotMesh
			needsUpdate = true
			break
		case len(meshList.Items) > 1:
			ctxToStsMap[ctx] = MeshStatusMisconfigured
			needsUpdate = true
			break
		}
		if needsUpdate {
			continue
		}
		mesh := meshList.Items[0]
		meshSpec := mesh.Spec
		clusterStatus := MeshStatusOK
		zonesChecked := map[string]bool{}
		for _, zoneInCluster := range meshSpec.Zones {
			expectedZoneInfo, found := ctxToZSpecMap[zoneInCluster.Name]
			if !found {
				clusterStatus = MeshStatusOutOfSync
				needsUpdate = true
				break
			}
			if expectedZoneInfo.ZoneName != zoneInCluster.ZoneName || expectedZoneInfo.MeshSyncAgentVip != zoneInCluster.MeshSyncAgentVip {
				clusterStatus = MeshStatusOutOfSync
				needsUpdate = true
				break
			}
			zonesChecked[zoneInCluster.Name] = true
		}
		if clusterStatus != MeshStatusOK {
			ctxToStsMap[ctx] = clusterStatus
			continue
		}
		ctxToStsMap[ctx] = clusterStatus
	}
	return ctxToStsMap, hasUnknown, needsUpdate
}

func (r *IstioClusterRegistry) checkAndCreateMeshCrd(ctx string) error {
	crdClientset, err := r.GetCrdClientset(ctx)
	if err != nil {
		return err
	}
	if err != nil {
		return err
	}
	li := v1.ListOptions{}
	crdList, err := crdClientset.ApiextensionsV1beta1().CustomResourceDefinitions().List(li)
	meshCrdFound := false
	for _, crd := range crdList.Items {
		if crd.GetName() == crv1.MeshResourceName {
			meshCrdFound = true
			break
		}
	}
	if !meshCrdFound {
		meshCrd := apiextensionsv1beta1.CustomResourceDefinition{}
		meshCrd.SetName(crv1.MeshResourceName)
		meshCrd.Spec.Group = crv1.MeshSpecGroup
		meshCrd.Spec.Version = crv1.MeshSpecVersion
		meshCrd.Spec.Scope = apiextensionsv1beta1.ClusterScoped
		meshCrd.Spec.Names.Kind = crv1.MeshResourceKind
		meshCrd.Spec.Names.Plural = crv1.MeshResourcePlural
		meshCrd.Spec.Names.Singular = crv1.MeshResourceSingular
		meshCrd.Spec.Names.ShortNames = []string{crv1.MeshShortName}
		_, err := crdClientset.ApiextensionsV1beta1().CustomResourceDefinitions().Create(&meshCrd)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *IstioClusterRegistry) checkAndCreateMeshResource(ctx, zoneName string, meshResource *crv1.Mesh) error {
	clientset, err := r.GetMeshClientset(ctx)
	if err != nil {
		return err
	}
	meshList, err := clientset.PkgV1().Meshs().List(v1.ListOptions{})
	if err != nil {
		meshList = &crv1.MeshList{}
		meshList.Items = []crv1.Mesh{}
	}
	meshUpdated := false
	for _, existingMeshRes := range meshList.Items {
		if existingMeshRes.GetName() != meshResource.GetName() {
			fmt.Printf("Correcting misconfigured cluster '%s' with cluster context '%s': Deleting old mesh config: %s\n", zoneName, ctx, existingMeshRes.GetName())
			clientset.PkgV1().Meshs().Delete(existingMeshRes.GetName(), &v1.DeleteOptions{})
			continue
		}
		identicalResource := false
		if len(existingMeshRes.Spec.Zones) == len(meshResource.Spec.Zones) {
			identicalResource = true
			for zidx, existingZone := range existingMeshRes.Spec.Zones {
				expectedZone := meshResource.Spec.Zones[zidx]
				if existingZone.Name != expectedZone.Name {
					identicalResource = false
					break
				}
				if existingZone.ZoneName != expectedZone.ZoneName {
					identicalResource = false
					break
				}
				if existingZone.MeshSyncAgentVip != expectedZone.MeshSyncAgentVip {
					identicalResource = false
					break
				}
				if existingZone.Activate != expectedZone.Activate {
					identicalResource = false
					break
				}
			}
		}
		if !identicalResource {
			fmt.Printf("Updating cluster '%s' with cluster context '%s': Updating mesh config: %s\n", zoneName, ctx, meshResource.Name)
			existingMesh, err := clientset.PkgV1().Meshs().Get(meshResource.Name, v1.GetOptions{})
			if err != nil {
				return err
			}
			meshResource.ResourceVersion = existingMesh.ResourceVersion
			_, err = clientset.PkgV1().Meshs().Update(meshResource)
			if err != nil {
				return err
			}
		}
		meshUpdated = true
	}
	if !meshUpdated {
		fmt.Printf("Updating cluster '%s' with cluster context '%s': Creating mesh config: %s\n", zoneName, ctx, meshResource.Name)
		_, err = clientset.PkgV1().Meshs().Create(meshResource)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *IstioClusterRegistry) UpdateCluster(ctx, zoneName string, sts MeshStatus, meshResource *crv1.Mesh) error {
	if sts == MeshStatusNotMesh {
		err := r.checkAndCreateMeshCrd(ctx)
		if err != nil {
			return err
		}
	}
	return r.checkAndCreateMeshResource(ctx, zoneName, meshResource)
}

func NewUpdateMeshError(em string) *UpdateMeshError {
	e := UpdateMeshError{em}
	return &e
}

func (e *UpdateMeshError) Error() string {
	return e.errorMsg
}

func NewIstioClusterRegistryError(em string) *IstioClusterRegistryError {
	e := IstioClusterRegistryError{em}
	return &e
}

func (e *IstioClusterRegistryError) Error() string {
	return e.errorMsg
}

func NewKubectlConfigRegistry() ClusterRegistry {
	var kubeconfig *string
	if home := homeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	return &KubectlConfigRegistry{kubeconfig, nil, nil, map[string]*rest.Config{}, map[string]*extv1.Clientset{}, map[string]*meshv1.Clientset{}}
}

// Gets a list of all clusters that are configurable
func (r *KubectlConfigRegistry) GetClusterContexts() map[string]bool {
	currentSet := map[string]bool{}
	r.pathOptions = clientcmd.NewDefaultPathOptions()
	var err error
	r.startingConfig, err = r.pathOptions.GetStartingConfig()
	if err != nil {
		fmt.Printf("Could not access Kubernetes' kubectl configuration: %s", err.Error())
		return currentSet
	}
	for ctxName, _ := range r.startingConfig.Contexts {
		currentSet[ctxName] = true
	}
	return currentSet
}

func (r *KubectlConfigRegistry) getCtxConfig(ctx string) (*rest.Config, error) {
	config, found := r.ctxConfigs[ctx]
	if found {
		return config, nil
	}
	r.startingConfig.CurrentContext = ctx
	clientcmd.ModifyConfig(r.pathOptions, *(r.startingConfig), true)

	r.startingConfig.CurrentContext = ctx
	clientcmd.ModifyConfig(r.pathOptions, *(r.startingConfig), true)
	return clientcmd.BuildConfigFromFlags("", *(r.kubeconfig))
}

func (r *KubectlConfigRegistry) GetMeshClientset(ctx string) (*meshv1.Clientset, error) {
	clientset, found := r.meshClientsets[ctx]
	if found {
		return clientset, nil
	}
	config, err := r.getCtxConfig(ctx)
	if err != nil {
		return nil, err
	}
	clientset, err = meshv1.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	r.meshClientsets[ctx] = clientset
	return clientset, nil
}

func (r *KubectlConfigRegistry) GetCrdClientset(ctx string) (*extv1.Clientset, error) {
	clientset, found := r.crdClientsets[ctx]
	if found {
		return clientset, nil
	}
	config, err := r.getCtxConfig(ctx)
	if err != nil {
		return nil, err
	}
	clientset, err = extv1.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	r.crdClientsets[ctx] = clientset
	return clientset, nil
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // windows
}
