package routes

import (
	"bytes"
	"encoding/json"
	"github.com/ReneKroon/ttlcache/v2"
	"github.com/go-logr/logr"
	"io"
	"k8s.io/client-go/informers"
	"net/http"
	"regexp"
	"ridecell-k8s-scheduler-extender/pkg/cache"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	applister "k8s.io/client-go/listers/apps/v1"
	goClinetCache "k8s.io/client-go/tools/cache"
	schedulerapi "k8s.io/kube-scheduler/extender/v1"
	// informerCache "ridecell-k8s-scheduler-extender/pkg/cache"
	// PodsPerNode "ridecell-k8s-scheduler-extender/pkg/routes/podspernode"
)

type PodsPerNode struct {
	podInformer      goClinetCache.SharedIndexInformer
	replicaSetLister applister.ReplicaSetLister
	log              logr.Logger
	ttlCache         *ttlcache.Cache
}

type PodData struct {
	replicaCount   int32
	maxPodsPerNode int
	deploymentName string
}

type Component struct {
	InformerFactory informers.SharedInformerFactory
	Log             logr.Logger
}

func NewPodsPerNode(informerFactort informers.SharedInformerFactory, logger logr.Logger) (b *PodsPerNode) {
	c := cache.NewPodsPerNode(informerFactort, logger)
	b = &PodsPerNode{
		podInformer:      c.PodInformer,
		replicaSetLister: c.ReplicaSetLister,
		log:              c.Log,
		ttlCache:         c.TTLCache,
	}
	return b
}

func (b *PodsPerNode) PodsPerNodeFilter(w http.ResponseWriter, r *http.Request) {
	var buf bytes.Buffer
	if r.Method != "POST" {
		return
	}
	body := io.TeeReader(r.Body, &buf)
	var extenderArgs schedulerapi.ExtenderArgs
	var extenderFilterResult *schedulerapi.ExtenderFilterResult

	if err := json.NewDecoder(body).Decode(&extenderArgs); err != nil {
		extenderFilterResult = &schedulerapi.ExtenderFilterResult{
			Error: err.Error(),
		}
	} else {
		extenderFilterResult = b.PodsPerNodeFilterHandler(extenderArgs)
	}

	if response, err := json.Marshal(extenderFilterResult); err != nil {
		b.log.Error(err, "error in json marshal")
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write(response)
		if err != nil {
			b.log.Error(err, "error in writing response")
		}
	}
}

func (p *PodsPerNode) PodsPerNodeFilterHandler(args schedulerapi.ExtenderArgs) *schedulerapi.ExtenderFilterResult {
	pod := args.Pod
	canSchedule := make([]corev1.Node, 0, len(args.Nodes.Items))
	canNotSchedule := make(map[string]string)
	var podNames []string

	p.log.Info("Request for Pod", "PodName", pod.Name)
	log := p.log.WithName(pod.Name)
	podData, valid := p.checkPod(pod)
	if !valid {
		result := schedulerapi.ExtenderFilterResult{
			Nodes:       args.Nodes,
			FailedNodes: canNotSchedule,
			Error:       "",
		}
		log.Info("Pod not valid")
		return &result
	}

	for _, node := range args.Nodes.Items {
		ok, msg := p.checkfitness(node, pod, podData)
		if ok {
			log.Info("can be schedule on node", "NodeName", node.Name)
			val, err := p.ttlCache.Get(node.Name)
			if err != nil && err != ttlcache.ErrNotFound {
				log.Error(err, "ttl Cache Error")
			}
			if err == ttlcache.ErrNotFound {
				podNames = append(podNames, pod.Name)
			} else {
				podNames = val.([]string)
				podNames = append(podNames, pod.Name)
			}
			err = p.ttlCache.Set(node.Name, podNames)
			if err != nil {
				log.Error(err, "Set ttl cache error")
			}
			log.Info("Pod added in ttlCache", "PodName", pod.Name)
			canSchedule = append(canSchedule, node)
			break
		} else {
			log.Info("Cannot schedule on node", "NodeName", node.Name, "Reason", msg)
			canNotSchedule[node.Name] = msg
		}
	}

	result := schedulerapi.ExtenderFilterResult{
		Nodes: &corev1.NodeList{
			Items: canSchedule,
		},
		FailedNodes: canNotSchedule,
		Error:       "",
	}
	return &result
}

func (p *PodsPerNode) checkPod(pod *corev1.Pod) (PodData, bool) {
	var maxPodsPerNode int
	var data PodData
	var replicasetName string
	var deploymentName string
	var valid bool = false
	log := p.log.WithName(pod.Name)
	//check if pod is of replicaSet
	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "ReplicaSet" {
			log.Info("Its of replicaset", "ReplicaSetName", owner.Name)
			replicasetName = owner.Name
			valid = true
			break
		}
	}
	if !valid {
		log.Info("Pod is not of ReplicaSet")
		return data, false
	}

	//get replicaset
	replicaSet, err := p.replicaSetLister.ReplicaSets(pod.Namespace).Get(replicasetName)
	if err != nil {
		log.Error(err, "Error getting replicaset from store")
		return data, false
	}
	if podsPerNode, ok := replicaSet.Annotations["k8s-scheduler-extender.ridecell.io/maxPodsPerNode"]; ok {
		if podsPerNode != "" {
			maxPodsPerNode, _ = strconv.Atoi(podsPerNode)
		} else {
			maxPodsPerNode = 2
		}
	} else {
		return data, false
	}

	//check if pod is of deployment
	for _, owner := range replicaSet.OwnerReferences {
		if owner.Kind == "Deployment" {
			log.Info("Deployment Name:", "DeploymentName", owner.Name)
			deploymentName = owner.Name
			valid = true
			break
		}
	}
	if !valid {
		log.Info("Pod is not of Deployment")
		return data, false
	}

	data.replicaCount = *replicaSet.Spec.Replicas
	data.maxPodsPerNode = maxPodsPerNode
	data.deploymentName = deploymentName
	log.Info("Deployment info", "MaxPods", maxPodsPerNode, "Replicacount", data.replicaCount)

	return data, true
}

func (p *PodsPerNode) checkfitness(node corev1.Node, pod *corev1.Pod, podData PodData) (bool, string) {
	var re *regexp.Regexp
	podsSet := make(map[string]string)
	podCount := 0

	log := p.log.WithName(pod.Name)

	re, _ = regexp.Compile(podData.deploymentName + "-(.*)")
	pods, err := p.podInformer.GetIndexer().ByIndex("nodename", node.Name)
	if err != nil {
		return false, err.Error()
	}
	// create set of pod names using both ttl cache and informer cache
	for _, pod := range pods {
		podsSet[pod.(*corev1.Pod).Name] = pod.(*corev1.Pod).Name
	}

	val, err := p.ttlCache.Get(node.Name)
	if err != nil && err != ttlcache.ErrNotFound {
		log.Error(err, "Failed to create ttl cache")
	}

	if val != nil {
		for _, podName := range val.([]string) {
			podsSet[podName] = podName
		}
	}

	for _, pod := range podsSet {
		if re.MatchString(pod) {
			podCount++
		}
	}

	if podData.replicaCount <= 3 {
		log.Info("Deployment info on node", "NodeName", node.Name, "PodCount", podCount)
		if podCount == 0 {
			return true, ""
		}
		return false, "Cannot schedule: Pod of deployment already exist."
	} else if podData.replicaCount > 3 {
		log.Info("Deployment info on node", "NodeName", node.Name, "PodCount", podCount)
		if podData.maxPodsPerNode > podCount {
			return true, ""
		}
		return false, "Cannot schedule: Maximum number of pods already running."
	}
	return false, "Other error"
}

// tree->
// /api/podpernode/ POST :-
// folder podspernode
// root.go -
