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
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	applister "k8s.io/client-go/listers/apps/v1"
	schedulerapi "k8s.io/kube-scheduler/extender/v1"
)

type Cache struct {
	podInformer      cache.SharedIndexInformer
	replicaSetLister applister.ReplicaSetLister
	customCache      *ttlcache.Cache
	log              logr.Logger
}

func BaseHandler(informerFactory informers.SharedInformerFactory, customCache *ttlcache.Cache, logger logr.Logger) *Cache {

	podInformer := informerFactory.Core().V1().Pods().Informer()
	log := logger.WithName("BaseHandler")
	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(new interface{}) {
			pod, ok := new.(*corev1.Pod)
			if !ok {
				log.Info("cannot convert to *v1.Pod:", new)
				return
			}
			log.Info("Added", "Pod:", pod.Name)
		},
		UpdateFunc: func(old, new interface{}) {
			pod, ok := old.(*corev1.Pod)
			if !ok {
				log.Info("cannot convert oldObj to", "*v1.Pod:", old)
				return
			}
			_, ok = new.(*corev1.Pod)
			if !ok {
				log.Info("cannot convert newObj to", "*v1.Pod:", new)
				return
			}
			log.Info("Updated", "Pod:", pod.Name)
		},
		DeleteFunc: func(old interface{}) {
			pod, ok := old.(*corev1.Pod)
			if !ok {
				log.Info("cannot convert to", "*v1.Pod:", old)
				return
			}
			log.Info("Deleted", "Pod:", pod.Name)
		},
	})
	//create indexer with index 'nodename'
	podInformer.AddIndexers(map[string]cache.IndexFunc{
		"nodename": func(obj interface{}) ([]string, error) {
			var nodeNames []string
			// log.Info("Pod: %v - Node: %v", obj.(*corev1.Pod).Name, obj.(*corev1.Pod).Spec.NodeName)
			nodeNames = append(nodeNames, obj.(*corev1.Pod).Spec.NodeName)
			return nodeNames, nil
		},
	})
	replicaSetInformer := informerFactory.Apps().V1().ReplicaSets().Informer()
	replicaSetInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(new interface{}) {
			replicaSet, ok := new.(*appsv1.ReplicaSet)
			if !ok {
				// log.Infof("Add:  Replica: %v| Ready replica: %v | Available replica: %v",replicaSet.Status.Replicas,replicaSet.Status.ReadyReplicas, replicaSet.Status.AvailableReplicas)

				log.Info("cannot convert to", "*appsv1.ReplicaSet:", new)
				return
			}
			log.Info("Added", "ReplicaSet:", replicaSet.Name)
		},
		UpdateFunc: func(old, new interface{}) {
			replicaSet, ok := old.(*appsv1.ReplicaSet)
			if !ok {
				// log.Infof("Replica: %v| Ready replica: %v | Available replica: %v",replicaSet.Status.Replicas,replicaSet.Status.ReadyReplicas, replicaSet.Status.AvailableReplicas)
				log.Info("cannot convert oldObj to", "*appsv1.replicaSet:", old)
				return
			}
			_, ok = new.(*appsv1.ReplicaSet)
			if !ok {
				log.Info("cannot convert newObj to", "*appsv1.replicaSet:", new)
				return
			}
			log.Info("Updated", "Replicaset:", replicaSet.Name)
		},
		DeleteFunc: func(old interface{}) {
			replicaSet, ok := old.(*appsv1.ReplicaSet)
			if !ok {
				log.Info("cannot convert to", "*appsv1.replicaSet:", old)
				return
			}
			log.Info("Deleted", "ReplicaSet:", replicaSet.Name)
		},
	})
	replicaSetLister := informerFactory.Apps().V1().ReplicaSets().Lister()
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
	canSchedule := make([]corev1.Node, 0, len(args.Nodes.Items))
	canNotSchedule := make(map[string]string)
	var podNames []string
	var maxPodsPerNode int
	c.log.Info("Request for Pod %v", pod.Name)
	log := c.log.WithName(pod.Name)

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
			log.Info("can be schedule on node", "NodeName:", pod.Name, node.Name)
			val, err := c.customCache.Get(node.Name)
			if err == ttlcache.ErrNotFound {
				podNames = append(podNames, pod.Name)
				c.customCache.Set(node.Name, podNames)
			} else {
				podNames = val.([]string)
				podNames = append(podNames, pod.Name)
			}
			c.customCache.Set(node.Name, podNames)
			canSchedule = append(canSchedule, node)
			break
		} else {
			log.Info("Cannot schedule on node", "NodeName:", node.Name, "Reason:", msg)
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
func (c *Cache) checkfitness(node corev1.Node, pod *corev1.Pod, maxPodsPerNode int, log logr.Logger) (bool, string) {
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
		return true, ""
	}
	//get replicaset
	replicaSet, _ := c.replicaSetLister.ReplicaSets(pod.Namespace).Get(replicasetName)
	//check if pod is of deployment
	for _, owner := range replicaSet.OwnerReferences {
		if owner.Kind == "Deployment" {
			log.Info("Its of Deployment %v", owner.Name)
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
		pods, err := c.podInformer.GetIndexer().ByIndex("nodename", node.Name)
		if err != nil {
			return false, err.Error()
		}
		val, err := c.customCache.Get(node.Name)
		if err == ttlcache.ErrNotFound {
			for _, pod := range pods {
				if re.MatchString(pod.(*corev1.Pod).Name) {
					podCount++
				}
			}
		} else {
			for _, podName := range val.([]string) {
				log.Info("Using custom cache")
				if re.MatchString(podName) {
					podCount++
				}
				log.Info("Custom cache: Count %v", podCount)
			}
		}
		log.Info("Deployment info on node", "NodeName", node.Name, "ReplicaCount:", replicaCount, "PodCount", podCount, "MaxPods", maxPodsPerNode)
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
				if re.MatchString(pod.(*corev1.Pod).Name) {
					podCount++
				}
			}
		} else {
			for _, podName := range val.([]string) {
				if re.MatchString(podName) {
					podCount++
				}
				log.Info("Using custom cache", "Count", podCount)
			}
		}
		log.Info("Deployment info on node", "NodeName", node.Name, "ReplicaCount:", replicaCount, "PodCount", podCount, "MaxPods", maxPodsPerNode)
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
		c.log.Error(err, "error in json marshal")
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(response)
	}
}
