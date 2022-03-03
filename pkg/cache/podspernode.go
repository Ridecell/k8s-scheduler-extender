package cache

import (
	"github.com/ReneKroon/ttlcache/v2"
	"k8s.io/client-go/tools/cache"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/client-go/listers/apps/v1"
)

// Creates a PodInformer and indexer, watches events and returns informer
func (c *Cache) GetPodInformer(ttlCache *ttlcache.Cache) cache.SharedIndexInformer {
	// watch events
	log := c.Log.WithName("Pod Informer")
	podInformer := c.InformerFactory.Core().V1().Pods().Informer()
	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(new interface{}) {
			pod, ok := new.(*corev1.Pod)
			if !ok {
				log.V(1).Info("cannot convert to *v1.Pod:", new)
				return
			}
			log.V(1).Info("Added", "Pod:", pod.Name)
		},
		UpdateFunc: func(old, new interface{}) {
			pod, ok := old.(*corev1.Pod)
			if !ok {
				log.V(1).Info("cannot convert oldObj to", "*v1.Pod:", old)
				return
			}
			_, ok = new.(*corev1.Pod)
			if !ok {
				log.V(1).Info("cannot convert newObj to", "*v1.Pod:", new)
				return
			}
			log.V(1).Info("Updated", "Pod:", pod.Name, "NodeName", pod.Spec.NodeName)
		},
		DeleteFunc: func(old interface{}) {
			pod, ok := old.(*corev1.Pod)
			if !ok {
				log.V(1).Info("cannot convert to", "*v1.Pod:", old)
				return
			}
			_ = ttlCache.Remove(pod.Spec.NodeName)
			log.V(1).Info("Deleted", "Pod", pod.Name, "NodeName", pod.Spec.NodeName)
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

// Creates a ReplicaSet informer, watches event and returns a Replicaset lister
func (ppn *Cache) GetReplicaSetLister() v1.ReplicaSetLister {
	log := ppn.Log.WithName("ReplicaSet Informer")
	replicaSetInformer := ppn.InformerFactory.Apps().V1().ReplicaSets().Informer()
	replicaSetInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(new interface{}) {
			replicaSet, ok := new.(*appsv1.ReplicaSet)
			if !ok {
				log.V(1).Info("cannot convert to", "*appsv1.ReplicaSet:", new)
				return
			}
			log.V(1).Info("Added", "ReplicaSet:", replicaSet.Name)
		},
		UpdateFunc: func(old, new interface{}) {
			replicaSet, ok := old.(*appsv1.ReplicaSet)
			if !ok {
				log.V(1).Info("cannot convert oldObj to", "*appsv1.replicaSet:", old)
				return
			}
			_, ok = new.(*appsv1.ReplicaSet)
			if !ok {
				log.V(1).Info("cannot convert newObj to", "*appsv1.replicaSet:", new)
				return
			}
			log.V(1).Info("Updated", "Replicaset:", replicaSet.Name)
		},
		DeleteFunc: func(old interface{}) {
			replicaSet, ok := old.(*appsv1.ReplicaSet)
			if !ok {
				log.V(1).Info("cannot convert to", "*appsv1.replicaSet:", old)
				return
			}
			log.V(1).Info("Deleted", "ReplicaSet:", replicaSet.Name)
		},
	})
	replicaSetLister := ppn.InformerFactory.Apps().V1().ReplicaSets().Lister()
	return replicaSetLister
}
