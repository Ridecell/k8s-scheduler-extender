package routes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"ridecell-k8s-scheduler-extender/pkg/cache"
	"strconv"

	"github.com/ReneKroon/ttlcache/v2"
	"github.com/go-logr/logr"
	"k8s.io/client-go/informers"

	corev1 "k8s.io/api/core/v1"
	applister "k8s.io/client-go/listers/apps/v1"
	goClinetCache "k8s.io/client-go/tools/cache"
	schedulerapi "k8s.io/kube-scheduler/extender/v1"
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

// Initializes informers and ttl cache
func NewPodsPerNode(informerFactort informers.SharedInformerFactory, logger logr.Logger) (b *PodsPerNode) {
	c := cache.NewPodsPerNodeCache(informerFactort, logger)
	b = &PodsPerNode{}
	b.log = c.Log
	b.replicaSetLister = c.GetReplicaSetLister()
	b.ttlCache = c.GetTTLCache()
	b.podInformer = c.GetPodInformer(b.ttlCache)
	return b
}

// Handles POST request received at '/podspernode/filter'
func (ppn *PodsPerNode) PodsPerNodeFilter(w http.ResponseWriter, r *http.Request) {
	var buf bytes.Buffer
	if r.Method != "POST" {
		return
	}
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
		if len(extenderFilterResult.Error)>0{
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
	pod := args.Pod
	canSchedule := make([]corev1.Node, 0, len(args.Nodes.Items))
	var podNames []string

	ppn.log.Info("Request for Pod", "PodName", pod.Name)
	log := ppn.log.WithName(pod.Name)
	podData, valid := ppn.checkPodDeployment(pod)
	if !valid {
		result := schedulerapi.ExtenderFilterResult{
			Nodes: args.Nodes,
			Error: "",
		}
		log.Info("Pod is not of deployment")
		return &result
	}

	for _, node := range args.Nodes.Items {
		ok, msg := ppn.checkfitness(node, pod, podData)
		if ok {
			log.Info("can be schedule on node", "NodeName", node.Name)
			val, err := ppn.ttlCache.Get(node.Name)
			if err != nil && err != ttlcache.ErrNotFound {
				log.Error(err, "ttl Cache Error")
			}
			if err == ttlcache.ErrNotFound {
				podNames = append(podNames, pod.Name)
			} else {
				podNames = val.([]string)
				podNames = append(podNames, pod.Name)
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

// checks if pods is of deployment set and returns pod data (replicaset name, deployment name)
func (ppn *PodsPerNode) checkPodDeployment(pod *corev1.Pod) (PodData, bool) {

	var maxPodsPerNode int
	var data PodData
	var replicasetName string
	var deploymentName string
	var valid bool = false
	log := ppn.log.WithName(pod.Name)
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
	replicaSet, err := ppn.replicaSetLister.ReplicaSets(pod.Namespace).Get(replicasetName)
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

//It checks if node is valid for pod to schedule
func (ppn *PodsPerNode) checkfitness(node corev1.Node, pod *corev1.Pod, podData PodData) (bool, string) {
	var re *regexp.Regexp
	podsSet := make(map[string]string)
	podCount := 0

	log := ppn.log.WithName(pod.Name)

	re, _ = regexp.Compile(podData.deploymentName + "-(.*)")
	pods, err := ppn.podInformer.GetIndexer().ByIndex("nodename", node.Name)
	if err != nil {
		return false, err.Error()
	}
	// create set of pod names using both ttl cache and informer cache
	for _, pod := range pods {
		podsSet[pod.(*corev1.Pod).Name] = pod.(*corev1.Pod).Name
	}

	val, err := ppn.ttlCache.Get(node.Name)
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
