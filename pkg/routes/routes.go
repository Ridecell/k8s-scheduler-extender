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
	"github.com/go-logr/logr"
	"k8s.io/client-go/tools/cache"

	v1 "k8s.io/api/core/v1"
	applister "k8s.io/client-go/listers/apps/v1"
	schedulerapi "k8s.io/kube-scheduler/extender/v1"
)

type Cache struct {
	PodInformer      cache.SharedIndexInformer
	ReplicaSetLister applister.ReplicaSetLister
	Log              logr.Logger
	// CustomCache      *ttlcache.Cache
}

type PodData struct {
	replicaCount   int32
	maxPodsPerNode int
	deploymentName string
}

// custom cache Setup
var customCache *ttlcache.Cache

func SetCache(cache *ttlcache.Cache) {
	customCache = cache
}
func UpdateCache(podName string, nodeName string, log logr.Logger) {
	val, err := customCache.Get(nodeName)
	if err != nil && err != ttlcache.ErrNotFound {
		log.Error(err, "Custom Cache Error")
	}
	if val != nil {
		podNames := val.([]string)
		for i, pod := range podNames {
			if pod == podName {
				podNames[i] = podNames[len(podNames)-1]
				podNames = podNames[:len(podNames)-1]
				err := customCache.Set(nodeName, podNames)
				if err != nil {
					log.Error(err, "Set cache error")
					return
				}
				log.Info("Custom Cache Updated", "Deleted Pod", podName, "Node", nodeName)
				return
			}
		}
	}
}

func (c *Cache) Index(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, "Welcome to k8s-scheduler-extender!\n")

}

func (c *Cache) FooFilter(w http.ResponseWriter, r *http.Request) {

	var buf bytes.Buffer
	body := io.TeeReader(r.Body, &buf)
	var extenderArgs schedulerapi.ExtenderArgs
	var extenderFilterResult *schedulerapi.ExtenderFilterResult
	if err := json.NewDecoder(body).Decode(&extenderArgs); err != nil {
		extenderFilterResult = &schedulerapi.ExtenderFilterResult{
			Error: err.Error(),
		}
	} else {
		extenderFilterResult = c.Handler(extenderArgs)
	}

	if response, err := json.Marshal(extenderFilterResult); err != nil {
		c.Log.Error(err, "error in json marshal")
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write(response)
		if err != nil {
			c.Log.Error(err, "error in writing response")
		}
	}
}

func (c *Cache) Handler(args schedulerapi.ExtenderArgs) *schedulerapi.ExtenderFilterResult {
	pod := args.Pod
	canSchedule := make([]v1.Node, 0, len(args.Nodes.Items))
	canNotSchedule := make(map[string]string)
	var podNames []string
	c.Log.Info("Request for Pod", "PodName", pod.Name)
	log := c.Log.WithName(pod.Name)
	podData, valid := c.checkPod(pod, log)
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
		ok, msg := c.checkfitness(node, pod, podData, log)
		if ok {
			log.Info("can be schedule on node", "NodeName", node.Name)
			val, err := customCache.Get(node.Name)
			if err != nil && err != ttlcache.ErrNotFound {
				log.Error(err, "Custom Cache Error")
			}
			if err == ttlcache.ErrNotFound {
				podNames = append(podNames, pod.Name)
			} else {
				podNames = val.([]string)
				podNames = append(podNames, pod.Name)
			}
			err = customCache.Set(node.Name, podNames)
			if err != nil {
				log.Error(err, "Set cache error")
			}
			log.Info("Pod added in customCache", "PodName", pod.Name)
			canSchedule = append(canSchedule, node)
			break
		} else {
			log.Info("Cannot schedule on node", "NodeName", node.Name, "Reason", msg)
			canNotSchedule[node.Name] = msg
		}
	}

	result := schedulerapi.ExtenderFilterResult{
		Nodes: &v1.NodeList{
			Items: canSchedule,
		},
		FailedNodes: canNotSchedule,
		Error:       "",
	}
	return &result
}

func (c *Cache) checkPod(pod *v1.Pod, log logr.Logger) (PodData, bool) {
	var maxPodsPerNode int
	var data PodData
	var replicasetName string
	var deploymentName string
	var valid bool = false

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
	replicaSet, err := c.ReplicaSetLister.ReplicaSets(pod.Namespace).Get(replicasetName)
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

func (c *Cache) checkfitness(node v1.Node, pod *v1.Pod, podData PodData, log logr.Logger) (bool, string) {
	var re *regexp.Regexp

	podsSet := make(map[string]string)
	podCount := 0

	re, _ = regexp.Compile(podData.deploymentName + "-(.*)")
	pods, err := c.PodInformer.GetIndexer().ByIndex("nodename", node.Name)
	if err != nil {
		return false, err.Error()
	}
	for _, pod := range pods {
		podsSet[pod.(*v1.Pod).Name] = pod.(*v1.Pod).Name
	}
	val, err := customCache.Get(node.Name)
	if err != nil && err != ttlcache.ErrNotFound {
		log.Error(err, "Custom Cache Error")
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
