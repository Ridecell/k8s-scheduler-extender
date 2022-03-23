package routes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/ReneKroon/ttlcache/v2"
	"github.com/Ridecell/k8s-scheduler-extender/pkg/cache"
	"go.uber.org/zap"
	"k8s.io/client-go/informers"

	corev1 "k8s.io/api/core/v1"
	applister "k8s.io/client-go/listers/apps/v1"
	k8scache "k8s.io/client-go/tools/cache"
	schedulerapi "k8s.io/kube-scheduler/extender/v1"
)

const (
	// If you are using this extender then you are meant to run more than 1 pod per node
	defaultMaxPodsPerNode = 2
)

type PodsPerNode struct {
	podInformer      k8scache.SharedIndexInformer
	replicaSetLister applister.ReplicaSetLister
	log              *zap.Logger
	ttlCache         *ttlcache.Cache
}

type PodData struct {
	replica        int32
	maxPodsPerNode int
	replicaSetName string
}

// Initializes ttl cache
func GetTTLCache(log *zap.Logger) *ttlcache.Cache {
	ttlCache := ttlcache.NewCache()
	// it takes 1-2 seconds to schedule a pod on a node, so the indexer doesn’t get updated immediately so need to maintain a temporary cache  for a minute
	err := ttlCache.SetTTL(time.Duration(1 * time.Minute))
	if err != nil {
		log.Error("Failed to create ttl cache", zap.String("Error", err.Error()))
	}
	return ttlCache
}

// Initializes informers
func NewPodsPerNode(informerFactory informers.SharedInformerFactory, logger *zap.Logger) (b *PodsPerNode) {
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

// Handles POST request received at 'api/podspernode/filter'
func (ppn *PodsPerNode) PodsPerNodeFilter(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		return
	}
	var buf bytes.Buffer
	body := io.TeeReader(r.Body, &buf)
	var extenderArgs schedulerapi.ExtenderArgs
	var extenderFilterResult *schedulerapi.ExtenderFilterResult

	if err := json.NewDecoder(body).Decode(&extenderArgs); err != nil {
		ppn.log.Error(err.Error())
		extenderFilterResult = &schedulerapi.ExtenderFilterResult{
			Nodes:       nil,
			FailedNodes: nil,
			Error:       err.Error(),
		}
	} else {
		extenderFilterResult = ppn.PodsPerNodeFilterHandler(extenderArgs)
	}

	if response, err := json.Marshal(extenderFilterResult); err != nil {
		ppn.log.Error("Error in writing response", zap.String("Error", err.Error()))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		errMsg := fmt.Sprintf("{'error':'%s'}", err.Error())
		_, err = w.Write([]byte(errMsg))
		if err != nil {
			ppn.log.Error("Error in writing response", zap.String("Error", err.Error()))
		}
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if len(extenderFilterResult.Error) > 0 {
			w.WriteHeader(http.StatusInternalServerError)
		}
		_, err := w.Write(response)
		if err != nil {
			ppn.log.Error("Error in writing response", zap.String("Error", err.Error()))
		}
	}
}

// It checks if pod is of ReplicaSet set and returns nodes on which it can be scheduled.
func (ppn *PodsPerNode) PodsPerNodeFilterHandler(args schedulerapi.ExtenderArgs) *schedulerapi.ExtenderFilterResult {
	var canSchedule []corev1.Node
	podData, yes := ppn.isSchedulable(args.Pod)
	if !yes {
		result := schedulerapi.ExtenderFilterResult{
			Nodes: args.Nodes,
			Error: "",
		}
		ppn.log.Info("Skipping Pod, annotation not present or its owner type is not a replicaset", zap.String("PodName", args.Pod.Name))
		return &result
	}
	var podNames []string
	for _, node := range args.Nodes.Items {
		msg, yes := ppn.canFit(node, args.Pod, podData)
		if yes {
			ppn.log.Info("Can be schedule on node", zap.String("NodeName", node.Name), zap.String("PodName", args.Pod.Name))
			val, err := ppn.ttlCache.Get(node.Name)
			if err != nil && err != ttlcache.ErrNotFound {
				ppn.log.Error("Failed to get ttlCache", zap.String("PodName", args.Pod.Name), zap.String("Error", err.Error()))
			}
			if err == ttlcache.ErrNotFound {
				podNames = append(podNames, args.Pod.Name)
			} else {
				podNames = val.([]string)
				podNames = append(podNames, args.Pod.Name)
			}
			err = ppn.ttlCache.Set(node.Name, podNames)
			if err != nil {
				ppn.log.Error("Failed to set ttl cache", zap.String("PodName", args.Pod.Name), zap.String("Error", err.Error()))
			}
			canSchedule = append(canSchedule, node)
			break
		} else {
			ppn.log.Info("Cannot schedule on node", zap.String("NodeName", node.Name), zap.String("PodName", args.Pod.Name), zap.String("Reason", msg))
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

// checks if pods have owner type ReplicaSet and returns pod data (replicaset name, replicaset name, maxpodCount)
func (ppn *PodsPerNode) isSchedulable(pod *corev1.Pod) (PodData, bool) {
	data := PodData{}
	isReplicaSet := false
	replicaSetName := ""

	// to reflect changes immediately we are adding annotation to pod
	if podsPerNode, yes := pod.Annotations["k8s-scheduler-extender.ridecell.io/maxPodsPerNode"]; yes {
		if maxPodsPerNode, err := strconv.Atoi(podsPerNode); err != nil {
			data.maxPodsPerNode = defaultMaxPodsPerNode
		} else {
			data.maxPodsPerNode = maxPodsPerNode
		}
	} else {
		return data, false
	}

	// Get replica name
	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "ReplicaSet" {
			replicaSetName = owner.Name
			isReplicaSet = true
			break
		}
	}
	if !isReplicaSet {
		ppn.log.Info("Pod do not have owner type ReplicaSet", zap.String("PodName", pod.Name))
		return data, false
	}

	//get replicaset
	replicaSet, err := ppn.replicaSetLister.ReplicaSets(pod.Namespace).Get(replicaSetName)
	if err != nil {
		ppn.log.Error("Failed to list replicaSet", zap.String("PodName", pod.Name), zap.String("Error", err.Error()))
		return data, false
	}

	data.replicaSetName = replicaSetName
	data.replica = *replicaSet.Spec.Replicas

	ppn.log.Info("Deployment data", zap.String("PodName", pod.Name), zap.String("ReplicaSetName", replicaSetName), zap.Int("MaxPods", data.maxPodsPerNode), zap.Int32("ReplicaCount", data.replica))
	return data, true
}

//It checks if node is valid for pod to schedule
func (ppn *PodsPerNode) canFit(node corev1.Node, pod *corev1.Pod, podData PodData) (string, bool) {
	podsOnNode, err := ppn.podInformer.GetIndexer().ByIndex("nodename", node.Name)
	if err != nil {
		return err.Error(), false
	}

	// create map of pod names using both ttl cache and informer cache. Here value key does not matter in map.
	// bcz We are using map as set here.
	podsSet := make(map[string]bool)
	for _, pod := range podsOnNode {
		podsSet[pod.(*corev1.Pod).Name] = true
	}

	ttlPods, err := ppn.ttlCache.Get(node.Name)
	if err != nil && err != ttlcache.ErrNotFound {
		ppn.log.Error("ttlCache error", zap.String("PodName", pod.Name), zap.String("Error", err.Error()))
	}

	if ttlPods != nil {
		for _, podName := range ttlPods.([]string) {
			podsSet[podName] = true
		}
	}

	podCount := 0
	var re *regexp.Regexp
	re, _ = regexp.Compile(podData.replicaSetName + "-(.*)")
	for pod := range podsSet {
		if re.MatchString(pod) {
			podCount++
		}
	}

	ppn.log.Info("Scheduling data for pod", zap.String("PodName", pod.Name), zap.String("ReplicaSetName", podData.replicaSetName), zap.String("NodeName", node.Name), zap.Int("PodCount", podCount), zap.Int32("ReplicaCount", podData.replica), zap.Int("MaxPods", podData.maxPodsPerNode))

	if podCount == 0 {
		return "", true
	} else {
		// if annotation value (maxPodsPerNode) is greater than or equal to replica count then schedule 1 pod per node
		if podData.maxPodsPerNode >= int(podData.replica) {
			if podCount > 0 {
				return fmt.Sprintf("Already running %d pods. Criteria not matched", podCount), false
			}
		}
		if podData.maxPodsPerNode > podCount {
			return "", true
		}
	}
	return "No criteria matched", false
}
