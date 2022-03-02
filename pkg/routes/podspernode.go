package routes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"

	"github.com/ReneKroon/ttlcache/v2"
	"github.com/Ridecell/k8s-scheduler-extender/pkg/cache"
	"github.com/go-logr/logr"
	"k8s.io/client-go/informers"

	corev1 "k8s.io/api/core/v1"
	applister "k8s.io/client-go/listers/apps/v1"
	k8scache "k8s.io/client-go/tools/cache"
	schedulerapi "k8s.io/kube-scheduler/extender/v1"
)

const (
	defaultMaxPodsPerNode = 1
	defaultMinPodsPerNode     = 3
)

type PodsPerNode struct {
	podInformer      k8scache.SharedIndexInformer
	replicaSetLister applister.ReplicaSetLister
	log              logr.Logger
	ttlCache         *ttlcache.Cache
}

type PodData struct {
	replica        int32
	maxPodsPerNode int
	deploymentName string
}

// Initializes informers and ttl cache
func NewPodsPerNode(informerFactory informers.SharedInformerFactory, logger logr.Logger) (b *PodsPerNode) {
	c := cache.Cache{
		Log:             logger,
		InformerFactory: informerFactory,
	}
	b = &PodsPerNode{}
	b.replicaSetLister = c.GetReplicaSetLister()
	b.ttlCache = c.GetTTLCache()
	b.podInformer = c.GetPodInformer(b.ttlCache)
	return b
}

// Handles POST request received at '/podspernode/filter'
func (ppn *PodsPerNode) PodsPerNodeFilter(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		return
	}
	var buf bytes.Buffer
	body := io.TeeReader(r.Body, &buf)
	var extenderArgs schedulerapi.ExtenderArgs
	var extenderFilterResult *schedulerapi.ExtenderFilterResult

	if err := json.NewDecoder(body).Decode(&extenderArgs); err != nil {
		ppn.log.Error(err, "error in json decode")
		extenderFilterResult = &schedulerapi.ExtenderFilterResult{
			Nodes:       nil,
			FailedNodes: nil,
			Error:       err.Error(),
		}
	} else {
		extenderFilterResult = ppn.PodsPerNodeFilterHandler(extenderArgs)
	}

	if response, err := json.Marshal(extenderFilterResult); err != nil {
		ppn.log.Error(err, "Error in json marshal")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		errMsg := fmt.Sprintf("{'error':'%s'}", err.Error())
		w.Write([]byte(errMsg))
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if len(extenderFilterResult.Error) > 0 {
			w.WriteHeader(http.StatusInternalServerError)
		}
		_, err := w.Write(response)
		if err != nil {
			ppn.log.Error(err, "Error in writing response")
		}
	}
}

// It checks if pod is of deployment set and returns nodes on which it can be scheduled.
func (ppn *PodsPerNode) PodsPerNodeFilterHandler(args schedulerapi.ExtenderArgs) *schedulerapi.ExtenderFilterResult {

	var canSchedule []corev1.Node

	ppn.log.Info("Request for Pod", "PodName", args.Pod.Name)
	log := ppn.log.WithName(args.Pod.Name)
	podData, yes := ppn.isOwnerDeployment(args.Pod)
	if !yes {
		result := schedulerapi.ExtenderFilterResult{
			Nodes: args.Nodes,
			Error: "",
		}
		log.Info("Pod is not of deployment")
		return &result
	}
	var podNames []string
	for _, node := range args.Nodes.Items {
		ok, msg := ppn.checkfitness(node, args.Pod, podData)
		if ok {
			log.Info("can be schedule on node", "NodeName", node.Name)
			val, err := ppn.ttlCache.Get(node.Name)
			if err != nil && err != ttlcache.ErrNotFound {
				log.Error(err, "ttl Cache Error")
			}
			if err == ttlcache.ErrNotFound {
				podNames = append(podNames, args.Pod.Name)
			} else {
				podNames = val.([]string)
				podNames = append(podNames, args.Pod.Name)
			}
			err = ppn.ttlCache.Set(node.Name, podNames)
			if err != nil {
				log.Error(err, "Set ttl cache error")
			}
			log.Info("Pod added in ttlCache", "PodName", pod.Name)
			canSchedule = append(canSchedule, node)
			break
		} else {
			log.Info("Cannot schedule on node", "NodeName", node.Name, "Reason", msg)
		}
	}

	result := schedulerapi.ExtenderFilterResult{
		Nodes: &corev1.NodeList{
			Items: canSchedule,
		},
		Error: "",
	}
	return &result
}

// checks if pods have owner type deployment set and returns pod data (replicaset name, deployment name)
func (ppn *PodsPerNode) isOwnerDeployment(pod *corev1.Pod) (PodData, bool) {
	data := PodData{}
	isDeployment := false
	replicasetName := ""
	log := ppn.log.WithName(pod.Name)
	// Get replica name
	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "ReplicaSet" {
			log.Info("Its of replicaset", "ReplicaSetName", owner.Name)
			replicasetName = owner.Name
			isDeployment = true
			break
		}
	}
	if !isDeployment {
		log.Info("Pod do not have owner type ReplicaSet")
		return data, false
	}

	//get replicaset
	replicaSet, err := ppn.replicaSetLister.ReplicaSets(pod.Namespace).Get(replicasetName)
	if err != nil {
		log.Error(err, "Error getting replicaset from store")
		return data, false
	}

	if podsPerNode, ok := replicaSet.Annotations["k8s-scheduler-extender.ridecell.io/maxPodsPerNode"]; ok {
		if podsPerNode != "" {
			data.maxPodsPerNode, _ = strconv.Atoi(podsPerNode)
		} else {
			data.maxPodsPerNode = defaultMaxPodsPerNode
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

	data.replica = *replicaSet.Spec.Replicas
	data.maxPodsPerNode = maxPodsPerNode
	data.deploymentName = deploymentName
	log.Info("Deployment info", "MaxPods", maxPodsPerNode, "Replicacount", data.replica)

	return data, true
}

//It checks if node is valid for pod to schedule
func (ppn *PodsPerNode) checkfitness(node corev1.Node, pod *corev1.Pod, podData PodData) (bool, string) {
	log := ppn.log.WithName(pod.Name)
	podsOnNode, err := ppn.podInformer.GetIndexer().ByIndex("nodename", node.Name)
	if err != nil {
		return false, err.Error()
	}

	// create map of pod names using both ttl cache and informer cache. Here value key does not matter in map.
	// bcz We are uisng map as set here.
	podsSet := make(map[string]bool)
	for _, pod := range podsOnNode {
		podsSet[pod.(*corev1.Pod).Name] = true

	}

	ttlPods, err := ppn.ttlCache.Get(node.Name)
	if err != nil && err != ttlcache.ErrNotFound {
		log.Error(err, "Failed get node from ttlcache")
	}

	if ttlPods != nil {
		for _, podName := range ttlPods.([]string) {
			podsSet[podName] = true
		}
	}

	podCount := 0
	var re *regexp.Regexp
	re, _ = regexp.Compile(podData.deploymentName + "-(.*)")
	for pod, _ := range podsSet {
		if re.MatchString(pod) {
			podCount++
		}
	}

	log.Info("Deployment info on node", "NodeName", node.Name, "PodCount", podCount)
	if podData.replica <= defaultMinPodsPerNode {
		if podCount == 0 {
			return true, ""
		}
		return false, "Cannot schedule: Pod of deployment already exist."
	}
	if podData.maxPodsPerNode > podCount {
		return true, ""
	}
	return false, "Cannot schedule: Maximum number of pods already running."

}
