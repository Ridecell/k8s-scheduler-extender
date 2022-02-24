package cache

import (
	"ridecell-k8s-scheduler-extender/pkg/routes"

	"github.com/ReneKroon/ttlcache/v2"
	"github.com/go-logr/logr"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func BaseHandler(informerFactory informers.SharedInformerFactory, customCache *ttlcache.Cache, logger logr.Logger) *routes.Cache {

	log := logger.WithName("Informer")
	// set custom cache
	routes.SetCache(customCache)
    
	// watch events
	podInformer := informerFactory.Core().V1().Pods().Informer()
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
			log.Info("Deleted", "Pod", pod.Name, "NodeName", pod.Spec.NodeName)
			// update custom cache
			routes.UpdateCache(pod.Name, pod.Spec.NodeName,logger)
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
	return &routes.Cache{
		PodInformer:      podInformer,
		ReplicaSetLister: replicaSetLister,
		// CustomCache:      customCache,
		Log: logger,
	}
}
