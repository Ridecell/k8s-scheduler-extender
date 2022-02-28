package cache

import (
	"github.com/ReneKroon/ttlcache/v2"
	"github.com/go-logr/logr"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/listers/apps/v1"
	"k8s.io/client-go/tools/cache"
)

type Cache struct {
	PodInformer      cache.SharedIndexInformer
	ReplicaSetLister v1.ReplicaSetLister
	Log              logr.Logger
	TTLCache         *ttlcache.Cache
	informerFactory  informers.SharedInformerFactory
}

func NewInformerCache(informerFactory informers.SharedInformerFactory, logger logr.Logger) (c *Cache) {
	c = &Cache{
		Log:             logger,
		informerFactory: informerFactory,
	}
	c.PodInformer = c.GetPodInformer()
	c.ReplicaSetLister = c.GetReplicaSetLister()
	c.TTLCache = c.getttlCache()
	return c
}
