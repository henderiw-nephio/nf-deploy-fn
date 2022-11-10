package transformer

import (
	"strconv"
	"strings"

	"github.com/GoogleContainerTools/kpt-functions-sdk/go/fn"
	"github.com/henderiw-nephio/nf-injector-controller/pkg/ipam"
	"github.com/henderiw-nephio/nf-injector-controller/pkg/upf"
	nfv1alpha1 "github.com/nephio-project/nephio-pocs/nephio-5gc-controller/apis/nf/v1alpha1"
	ipamv1alpha1 "github.com/nokia/k8s-ipam/apis/ipam/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

type NfDeploy struct {
	namespace string
	region    string
	dnn       string
	capacity  nfv1alpha1.UPFCapacity
	endpoints map[string]*nfv1alpha1.Endpoint
	n6pool    nfv1alpha1.Pool
}

func Run(rl *fn.ResourceList) (bool, error) {
	t := &NfDeploy{
		endpoints: map[string]*nfv1alpha1.Endpoint{
			"n3": nil,
			"n4": nil,
			"n6": nil,
			"n9": nil,
		},
	}
	// gathers the ip info from the ip-allocations
	t.GatherInfo(rl)

	// transforms the upf with the ip info collected/gathered
	t.GenerateNfDeploy(rl)
	return true, nil
}

func (t *NfDeploy) GatherInfo(rl *fn.ResourceList) {
	for _, o := range rl.Items {
		// parse the node using kyaml
		rn, err := yaml.Parse(o.String())
		if err != nil {
			rl.Results = append(rl.Results, fn.ErrorConfigObjectResult(err, o))
		}
		if rn.GetApiVersion() == "nf.nephio.org/v1alpha1" && rn.GetKind() == "FiveGCoreTopology" {
			t.namespace = rn.GetNamespace()
			if t.region, err = upf.GetRegion(rn); err != nil {
				rl.Results = append(rl.Results, fn.ErrorConfigObjectResult(err, o))
			}
			t.dnn = upf.GetDnn(rn)
			t.capacity = upf.GetCapacity(rn)
			for epName := range t.endpoints {
				if epName == "n6" {
					// it is assumed n6 is needed this i why an err is returned, when n6 is not found
					n6ep, err := upf.GetN6Endpoint(epName, rn)
					if err != nil {
						rl.Results = append(rl.Results, fn.ErrorConfigObjectResult(err, o))
					}
					t.endpoints[epName] = &n6ep.Endpoint
					t.n6pool = n6ep.UEPool
				} else {
					ep, err := upf.GetEndpoint(epName, rn)
					if err != nil {
						rl.Results = append(rl.Results, fn.ErrorConfigObjectResult(err, o))
					} else {
						t.endpoints[epName] = ep
					}
				}
			}
		}
	}
}

func (t *NfDeploy) GenerateNfDeploy(rl *fn.ResourceList) {
	for epName, ep := range t.endpoints {
		if *ep.NetworkInstance != "" && *ep.NetworkName != "" {
			ipAlloc, err := ipam.BuildIPAMAllocationFn(
				strings.Join([]string{"upf", t.region}, "-"),
				types.NamespacedName{
					Name:      epName,
					Namespace: t.namespace,
				},
				ipamv1alpha1.IPAllocationSpec{
					PrefixKind: string(ipamv1alpha1.PrefixKindNetwork),
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							ipamv1alpha1.NephioNetworkInstanceKey: *ep.NetworkInstance,
							ipamv1alpha1.NephioNetworkNameKey:     *ep.NetworkName,
						},
					},
				})
			if err != nil {
				rl.Results = append(rl.Results, fn.ErrorConfigObjectResult(err, ipAlloc))
			}
			rl.Items = append(rl.Items, ipAlloc)
		}
	}

	ps, err := strconv.Atoi(*t.n6pool.PrefixSize)
	if err != nil {
		rl.Results = append(rl.Results, fn.ErrorConfigObjectResult(err, rl.Items[0]))
	}
	ipPoolAlloc, err := ipam.BuildIPAMAllocationFn(
		strings.Join([]string{"upf", t.region}, "-"),
		types.NamespacedName{
			Name:      "n6pool",
			Namespace: t.namespace,
		},
		ipamv1alpha1.IPAllocationSpec{
			PrefixKind:   string(ipamv1alpha1.PrefixKindPool),
			PrefixLength: uint8(ps),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					ipamv1alpha1.NephioNetworkInstanceKey: *t.n6pool.NetworkInstance,
					ipamv1alpha1.NephioNetworkNameKey:     *t.n6pool.NetworkName,
				},
			},
		})
	if err != nil {
		rl.Results = append(rl.Results, fn.ErrorConfigObjectResult(err, ipPoolAlloc))
	}
	rl.Items = append(rl.Items, ipPoolAlloc)

	upfDeployment, err := upf.BuildUPFDeploymentFn(
		types.NamespacedName{
			Name:      strings.Join([]string{"upf", t.region}, "-"),
			Namespace: t.namespace,
		},
		upf.BuildUPFDeploymentSpec(t.endpoints, t.dnn, t.capacity),
	)
	if err != nil {
		rl.Results = append(rl.Results, fn.ErrorConfigObjectResult(err, upfDeployment))
	}
	rl.Items = append(rl.Items, upfDeployment)
}
