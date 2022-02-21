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
	"k8s.io/client-go/tools/cache"

	v1 "k8s.io/api/core/v1"
	applister "k8s.io/client-go/listers/apps/v1"
	log "k8s.io/klog/v2"
	schedulerapi "k8s.io/kube-scheduler/extender/v1"
)

type Cache struct {
	podInformer      cache.SharedIndexInformer
	replicaSetLister applister.ReplicaSetLister
	customCache      *ttlcache.Cache
}

func InitCache(podInformer cache.SharedIndexInformer, replicaSetLister applister.ReplicaSetLister, customCache *ttlcache.Cache) *Cache {
	return &Cache{
		podInformer:      podInformer,
		replicaSetLister: replicaSetLister,
		customCache:      customCache,
	}
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

	log.Infof("Request for Pod %v", pod.Name)
	if pod.Annotations != nil {
		if podsPerNode, ok := pod.Annotations["podspernode"]; ok {
			maxPodsPerNode, _ = strconv.Atoi(podsPerNode)
		}
	} else {
		maxPodsPerNode = 2
	}
	for _, node := range args.Nodes.Items {

		ok, msg := c.checkfitness(node, pod, maxPodsPerNode)
		if ok == true {
			log.Infof("Pod %s can be schedule on node %s \n", pod.Name, node.Name)
			val, err := c.customCache.Get(node.Name)
			if err == ttlcache.ErrNotFound {
				podNames = append(podNames, pod.Name)
				c.customCache.Set(node.Name, podNames)
				log.Infof("podNames %v", podNames)
			} else {
				podNames = val.([]string)
				podNames = append(podNames, pod.Name)
				log.Infof("podNames %v", podNames)
			}

			c.customCache.Set(node.Name, podNames)
			canSchedule = append(canSchedule, node)
			break
		} else {
			log.Infof("Pod %s cannot schedule on node %s | Reason: %s \n", pod.Name, node.Name, msg)
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
func (c *Cache) checkfitness(node v1.Node, pod *v1.Pod, maxPodsPerNode int) (bool, string) {
	var replicasetName string
	var deploymentName string
	var valid bool = false
	//check if pod is of replicaSet
	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "ReplicaSet" {
			log.Infof("Its of replicaset %s", owner.Name)

			replicasetName = owner.Name
			valid = true
			break
		}
	}

	if !valid {
		log.Infof("Pod is not of ReplicaSet")
		return true, ""
	}
	//get replicaset
	replicaSet, _ := c.replicaSetLister.ReplicaSets(pod.Namespace).Get(replicasetName)
	//check if pod is of deployment
	for _, owner := range replicaSet.OwnerReferences {
		if owner.Kind == "Deployment" {
			log.Infof("Its of Deployment %v", owner.Name)
			deploymentName = owner.Name
			valid = true
			break
		}
	}
	if !valid {
		log.Infof("Pod is not of Deployment")
		return true, ""
	}

	replicaCount := *replicaSet.Spec.Replicas
	// maxPodsPerNode := deployment.Annotations[""]

	podCount := 0
	var re *regexp.Regexp
	re, _ = regexp.Compile(deploymentName + "-(.*)")
	if replicaCount <= 3 {
		pods, err := c.podInformer.GetIndexer().ByIndex("nodename", node.Name)
		if err != nil {
			return false, err.Error()
		}
		val, err := c.customCache.Get(node.Name)
		if err == ttlcache.ErrNotFound {
			for _, pod := range pods {
				log.Info("Checking pod %v", pod.(*v1.Pod).Name)
				if re.MatchString(pod.(*v1.Pod).Name) {
					podCount++
				}
			}
		} else {
			for _, podName := range val.([]string) {
				log.Infof("Entered for custom cache")
				if re.MatchString(podName) {
					podCount++
				}
				log.Infof("Entered for custom cache: Count %v", podCount)
			}
		}
		log.Infof("Pod Count: %v", podCount)
		if podCount == 0 {
			return true, ""
		}
		return false, "Cannot schedule: Pod of deployment already exist."
	} else if replicaCount > 3 {
		pods, err := c.podInformer.GetIndexer().ByIndex("nodename", node.Name)
		if err != nil {
			return false, err.Error()
		}

		val, err := c.customCache.Get(node.Name)
		if err == ttlcache.ErrNotFound {
			for _, pod := range pods {
				log.Info("Checking pod %v", pod.(*v1.Pod).Name)
				if re.MatchString(pod.(*v1.Pod).Name) {
					podCount++
				}
			}
		} else {
			for _, podName := range val.([]string) {
				log.Infof("Entered for custom cache")
				if re.MatchString(podName) {
					podCount++
				}
				log.Infof("Entered for custom cache: Count %v", podCount)
			}
		}
		log.Infof("replicacount: %v |Pod Count >3: %v | maxpods: %v", replicaCount, podCount, maxPodsPerNode)
		if maxPodsPerNode > podCount {
			return true, ""
		}
		return false, "Cannot schedule: Maximum number of pods already running."
	}
	return false, "Some error"
}

func (c *Cache) Filter(w http.ResponseWriter, r *http.Request) {

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
		log.Fatalln(err)
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(response)
	}
}
