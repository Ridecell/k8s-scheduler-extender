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
	CustomCache      *ttlcache.Cache
	Log              logr.Logger
}

func (c *Cache) Index(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, "Welcome to k8s-scheduler-extender!\n")

}

func (c *Cache) Handler(args schedulerapi.ExtenderArgs) *schedulerapi.ExtenderFilterResult {
	pod := args.Pod
	canSchedule := make([]v1.Node, 0, len(args.Nodes.Items))
	canNotSchedule := make(map[string]string)
	var podNames []string
	var maxPodsPerNode int
	c.Log.Info("Request for Pod", "PodName", pod.Name)
	log := c.Log.WithName(pod.Name)

	if pod.Annotations != nil {
		if podsPerNode, ok := pod.Annotations["podspernode"]; ok {
			maxPodsPerNode, _ = strconv.Atoi(podsPerNode)
		}
	} else {
		maxPodsPerNode = 2
	}
	for _, node := range args.Nodes.Items {

		ok, msg := c.checkfitness(node, pod, maxPodsPerNode, log)
		if ok {
			log.Info("can be schedule on node", "NodeName", node.Name)
			val, err := c.CustomCache.Get(node.Name)
			if err != nil && err != ttlcache.ErrNotFound {
				log.Error(err, "Custom Cache Error")
			}
			if err == ttlcache.ErrNotFound {
				podNames = append(podNames, pod.Name)
				c.CustomCache.Set(node.Name, podNames)
			} else {
				podNames = val.([]string)
				podNames = append(podNames, pod.Name)
			}
			c.CustomCache.Set(node.Name, podNames)
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

func (c *Cache) checkfitness(node v1.Node, pod *v1.Pod, maxPodsPerNode int, log logr.Logger) (bool, string) {
	var replicasetName string
	var deploymentName string
	var valid bool = false
	podsSet := make(map[string]string)
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
		return true, ""
	}
	//get replicaset
	replicaSet, _ := c.ReplicaSetLister.ReplicaSets(pod.Namespace).Get(replicasetName)
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
		return true, ""
	}

	replicaCount := *replicaSet.Spec.Replicas
	// maxPodsPerNode := deployment.Annotations[""]

	podCount := 0
	var re *regexp.Regexp
	re, _ = regexp.Compile(deploymentName + "-(.*)")
	if replicaCount <= 3 {
		pods, err := c.PodInformer.GetIndexer().ByIndex("nodename", node.Name)
		if err != nil {
			return false, err.Error()
		}
		for _, pod := range pods {
			podsSet[pod.(*v1.Pod).Name] = pod.(*v1.Pod).Name
		}
		val, err := c.CustomCache.Get(node.Name)
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
		log.Info("Deployment info on node", "NodeName", node.Name, "ReplicaCount", replicaCount, "PodCount", podCount, "MaxPods", maxPodsPerNode)
		if podCount == 0 {
			return true, ""
		}
		return false, "Cannot schedule: Pod of deployment already exist."
	} else if replicaCount > 3 {
		pods, err := c.PodInformer.GetIndexer().ByIndex("nodename", node.Name)
		if err != nil {
			return false, err.Error()
		}
		for _, pod := range pods {
			podsSet[pod.(*v1.Pod).Name] = pod.(*v1.Pod).Name
		}
		val, err := c.CustomCache.Get(node.Name)
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
		log.Info("Deployment info on node", "NodeName", node.Name, "ReplicaCount", replicaCount, "PodCount", podCount, "MaxPods", maxPodsPerNode)
		
		if maxPodsPerNode > podCount {
			return true, ""
		}
		return false, "Cannot schedule: Maximum number of pods already running."
	}
	return false, "Other error"
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
		w.Write(response)
	}
}
