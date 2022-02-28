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

type Cache struct {
	PodInformer      cache.SharedIndexInformer
	ReplicaSetLister v1.ReplicaSetLister
	Log              logr.Logger
	TTLCache         *ttlcache.Cache
	informerFactory  informers.SharedInformerFactory
}

func NewInformerCache(informerFactory informers.SharedInformerFactory, logger logr.Logger) (c *Cache) {
	log := logger.WithName("Informer")
	//init temprory cache
	ttlCache := ttlcache.NewCache()
	// it takes 1-2 seconds to schedule a pod on a node, so the indexer doesn’t get updated immediately so need to maintain a temporary cache  for a minute
	err := ttlCache.SetTTL(time.Duration(1 * time.Minute))
	if err != nil {
		log.Error(err, "Failed to create ttl cache")
	}
	c = &Cache{
		Log:             logger,
		informerFactory: informerFactory,
		TTLCache: ttlCache,
	}
	c.PodInformer = c.GetPodInformer()
	c.ReplicaSetLister = c.GetReplicaSetLister()
	return c
}

func (c *Cache) GetPodInformer() cache.SharedIndexInformer {
	// watch events
	podInformer := c.informerFactory.Core().V1().Pods().Informer()
	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(new interface{}) {
			pod, ok := new.(*corev1.Pod)
			if !ok {
				c.Log.Info("cannot convert to *v1.Pod:", new)
				return
			}
			c.Log.Info("Added", "Pod:", pod.Name)
		},
		UpdateFunc: func(old, new interface{}) {
			pod, ok := old.(*corev1.Pod)
			if !ok {
				c.Log.Info("cannot convert oldObj to", "*v1.Pod:", old)
				return
			}
			_, ok = new.(*corev1.Pod)
			if !ok {
				c.Log.Info("cannot convert newObj to", "*v1.Pod:", new)
				return
			}
			c.Log.Info("Updated", "Pod:", pod.Name, "NodeName", pod.Spec.NodeName)
		},
		DeleteFunc: func(old interface{}) {
			pod, ok := old.(*corev1.Pod)
			if !ok {
				c.Log.Info("cannot convert to", "*v1.Pod:", old)
				return
			}
			c.Log.Info("Deleted", "Pod", pod.Name, "NodeName", pod.Spec.NodeName)
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
		c.Log.Error(err, "Informer error")
	}
	return podInformer
}

func (c *Cache) GetReplicaSetLister() v1.ReplicaSetLister {
	replicaSetInformer := c.informerFactory.Apps().V1().ReplicaSets().Informer()
	replicaSetInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(new interface{}) {
			replicaSet, ok := new.(*appsv1.ReplicaSet)
			if !ok {
				c.Log.Info("cannot convert to", "*appsv1.ReplicaSet:", new)
				return
			}
			c.Log.Info("Added", "ReplicaSet:", replicaSet.Name)
		},
		UpdateFunc: func(old, new interface{}) {
			replicaSet, ok := old.(*appsv1.ReplicaSet)
			if !ok {
				c.Log.Info("cannot convert oldObj to", "*appsv1.replicaSet:", old)
				return
			}
			_, ok = new.(*appsv1.ReplicaSet)
			if !ok {
				c.Log.Info("cannot convert newObj to", "*appsv1.replicaSet:", new)
				return
			}
			c.Log.Info("Updated", "Replicaset:", replicaSet.Name)
		},
		DeleteFunc: func(old interface{}) {
			replicaSet, ok := old.(*appsv1.ReplicaSet)
			if !ok {
				c.Log.Info("cannot convert to", "*appsv1.replicaSet:", old)
				return
			}
			c.Log.Info("Deleted", "ReplicaSet:", replicaSet.Name)
		},
	})
	replicaSetLister := c.informerFactory.Apps().V1().ReplicaSets().Lister()
	return replicaSetLister
}
