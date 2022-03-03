package routes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

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
	// If you are using this extender then you are meant to run more than 1 pod per node
	defaultMaxPodsPerNode = 2
	defaultMinPodsPerNode = 1
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

// Initializes ttl cache
func GetTTLCache(logger logr.Logger) *ttlcache.Cache {
	log := logger.WithName("ttl Cache")
	ttlCache := ttlcache.NewCache()
	// it takes 1-2 seconds to schedule a pod on a node, so the indexer doesn’t get updated immediately so need to maintain a temporary cache  for a minute
	err := ttlCache.SetTTL(time.Duration(1 * time.Minute))
	if err != nil {
		log.Error(err, "Failed to create ttl cache")
	}
	return ttlCache
}

// Initializes informers
func NewPodsPerNode(informerFactory informers.SharedInformerFactory, logger logr.Logger) (b *PodsPerNode) {
	c := cache.Cache{
		Log:             logger,
		InformerFactory: informerFactory,
	}
	b = &PodsPerNode{}
	b.log = logger
	b.replicaSetLister = c.GetReplicaSetLister()
	b.ttlCache = GetTTLCache(logger)
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
	podData, yes := ppn.isSchedulable(args.Pod)
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
		msg, ok := ppn.canFit(node, args.Pod, podData)
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
			log.Info("Pod added in ttlCache", "PodName", args.Pod.Name)
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
func (ppn *PodsPerNode) isSchedulable(pod *corev1.Pod) (PodData, bool) {
	data := PodData{}
	isDeployment := false
	replicasetName := ""
	log := ppn.log.WithName(pod.Name)

	// to reflect changes immediately we are adding annotation to pod
	if podsPerNode, ok := pod.Annotations["k8s-scheduler-extender.ridecell.io/maxPodsPerNode"]; ok {
		if podsPerNode != "" {
			data.maxPodsPerNode, _ = strconv.Atoi(podsPerNode)
		} else {
			data.maxPodsPerNode = defaultMaxPodsPerNode
		}
	} else {
		return data, false
	}

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

	// extract deployment name from prelicaset name
	data.deploymentName = replicasetName[:strings.LastIndexByte(replicasetName, '-')]

	data.replica = *replicaSet.Spec.Replicas
	log.Info("Deployment info", "MaxPods", data.maxPodsPerNode, "Replicacount", data.replica)
	return data, true
}

//It checks if node is valid for pod to schedule
func (ppn *PodsPerNode) canFit(node corev1.Node, pod *corev1.Pod, podData PodData) (string, bool) {
	log := ppn.log.WithName(pod.Name)
	podsOnNode, err := ppn.podInformer.GetIndexer().ByIndex("nodename", node.Name)
	if err != nil {
		return err.Error(), false
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
	for pod, ok := range podsSet {
		if ok && re.MatchString(pod) {
			podCount++
		}
	}

	log.Info("Deployment info on node", "NodeName", node.Name, "PodCount", podCount, "replica Count",podData.replica,"maxPods",podData.maxPodsPerNode)
	// if replica count - defaultmaxpodspernode >= defaultMinPodsPerNode then pods should be schedule as one pod per node
	// eg. If replica count is 2, then (2-2) = 0 <=1 -> true then pods should be scheduled on separate nodes
	if podData.replica <= defaultMinPodsPerNode || defaultMinPodsPerNode >= (podData.replica-defaultMaxPodsPerNode) {
		if podCount == 0 {
			return "", true
		}
		return "Cannot schedule: Already running default minimum pods: "+strconv.Itoa(defaultMinPodsPerNode), false
	}

	if podData.maxPodsPerNode > podCount {
		return "", true
	}
	return "Cannot schedule:Already running maximum number of pods:"+strconv.Itoa(podData.maxPodsPerNode), false
}
