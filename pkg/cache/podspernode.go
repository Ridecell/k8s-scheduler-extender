package cache

import (
	"time"

	"github.com/ReneKroon/ttlcache/v2"
	"github.com/go-logr/logr"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/listers/apps/v1"
	"k8s.io/client-go/tools/cache"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

type PodsPerNode struct {
	PodInformer      cache.SharedIndexInformer
	ReplicaSetLister v1.ReplicaSetLister
	Log              logr.Logger
	TTLCache         *ttlcache.Cache
	InformerFactory  informers.SharedInformerFactory
}

func NewPodsPerNode(informerFactory informers.SharedInformerFactory, logger logr.Logger) (c *PodsPerNode) {
	c = &PodsPerNode{
		Log:             logger,
		InformerFactory: informerFactory,
	}
	c.PodInformer = c.GetPodInformer()
	c.ReplicaSetLister = c.GetReplicaSetLister()
	c.TTLCache = c.getttlCache()
	return c
}

func (c *PodsPerNode) GetPodInformer() cache.SharedIndexInformer {
	// watch events
	log := c.Log.WithName("Pod Informer")
	podInformer := c.InformerFactory.Core().V1().Pods().Informer()
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
			log.Info("Updated", "Pod:", pod.Name, "NodeName", pod.Spec.NodeName)
		},
		DeleteFunc: func(old interface{}) {
			pod, ok := old.(*corev1.Pod)
			if !ok {
				log.Info("cannot convert to", "*v1.Pod:", old)
				return
			}
			_ = c.TTLCache.Remove(pod.Spec.NodeName)
			log.Info("Deleted", "Pod", pod.Name, "NodeName", pod.Spec.NodeName)
		},
	})
	//create indexer with index 'nodename'
	err := podInformer.AddIndexers(map[string]cache.IndexFunc{
		"nodename": func(obj interface{}) ([]string, error) {
			var nodeNames []string
			nodeNames = append(nodeNames, obj.(*corev1.Pod).Spec.NodeName)
			return nodeNames, nil
		},
	})
	if err != nil {
		log.Error(err, "Informer error")
	}

	return podInformer
}

func (c *PodsPerNode) GetReplicaSetLister() v1.ReplicaSetLister {
	log := c.Log.WithName("ReplicaSet Informer")
	replicaSetInformer := c.InformerFactory.Apps().V1().ReplicaSets().Informer()
	replicaSetInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(new interface{}) {
			replicaSet, ok := new.(*appsv1.ReplicaSet)
			if !ok {
				log.Info("cannot convert to", "*appsv1.ReplicaSet:", new)
				return
			}
			log.Info("Added", "ReplicaSet:", replicaSet.Name)
		},
		UpdateFunc: func(old, new interface{}) {
			replicaSet, ok := old.(*appsv1.ReplicaSet)
			if !ok {
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
	replicaSetLister := c.InformerFactory.Apps().V1().ReplicaSets().Lister()
	return replicaSetLister
}

func (c *PodsPerNode) getttlCache() *ttlcache.Cache {
	log := c.Log.WithName("ttl Cache")
	ttlCache := ttlcache.NewCache()
	// it takes 1-2 seconds to schedule a pod on a node, so the indexer doesn’t get updated immediately so need to maintain a temporary cache  for a minute
	err := ttlCache.SetTTL(time.Duration(1 * time.Minute))
	if err != nil {
		log.Error(err, "Failed to create ttl cache")
	}
	return ttlCache
}