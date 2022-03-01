package cache

import (
	"k8s.io/client-go/listers/apps/v1"
	"k8s.io/client-go/tools/cache"

	appsv1 "k8s.io/api/apps/v1"
)

func (c *Cache) GetReplicaSetLister() v1.ReplicaSetLister {
	log:=c.Log.WithName("ReplicaSet Informer")
	replicaSetInformer := c.informerFactory.Apps().V1().ReplicaSets().Informer()
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
	replicaSetLister := c.informerFactory.Apps().V1().ReplicaSets().Lister()
	return replicaSetLister
}
