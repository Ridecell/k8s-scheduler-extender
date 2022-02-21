package main

import (
	"net/http"
	"os"
	"path/filepath"
	"ridecell-k8s-scheduler-extender/pkg/routes"
	"time"

	"github.com/ReneKroon/ttlcache/v2"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kubernetes "k8s.io/client-go/kubernetes"
	clientcmd "k8s.io/client-go/tools/clientcmd"
	log "k8s.io/klog/v2"
)

func connectToK8s() *kubernetes.Clientset {
	home, exists := os.LookupEnv("HOME")
	if !exists {
		home = "/root"
	}

	configPath := filepath.Join(home, ".kube", "config")

	config, err := clientcmd.BuildConfigFromFlags("", configPath)
	if err != nil {
		config, err = rest.InClusterConfig()
		if err != nil {
			log.Fatalln("failed to create K8s config: ", err.Error())
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalln("Failed to create K8s clientset")
	}

	return clientset
}

func main() {

	customCache := ttlcache.NewCache()
	customCache.SetTTL(time.Duration(2 * time.Second))

	clientset := connectToK8s()
	stopCh := make(chan struct{})
	informerFactory := informers.NewSharedInformerFactory(clientset, 10*time.Minute)

	podInformer := informerFactory.Core().V1().Pods().Informer()
	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(new interface{}) {
			pod, ok := new.(*corev1.Pod)
			if !ok {
				log.Warningf("cannot convert to *v1.Pod: %v", new)
				return
			}
			log.Infof("Pod %s Added", pod.Name)
		},
		UpdateFunc: func(old, new interface{}) {
			pod, ok := old.(*corev1.Pod)
			if !ok {
				log.Warningf("cannot convert oldObj to *v1.Pod: %v", old)
				return
			}
			_, ok = new.(*corev1.Pod)
			if !ok {
				log.Warningf("cannot convert newObj to *v1.Pod: %v", new)
				return
			}
			log.Infof("Pod %s Updated", pod.Name)
		},
		DeleteFunc: func(old interface{}) {
			pod, ok := old.(*corev1.Pod)
			if !ok {
				log.Warningf("cannot convert to *v1.Pod: %v", old)
				return
			}
			log.Infof("Pod %s Deleted", pod.Name)
		},
	})
	//create indexer with index 'nodename'
	podInformer.AddIndexers(map[string]cache.IndexFunc{
		"nodename": func(obj interface{}) ([]string, error) {
			var nodeNames []string
			log.Infof("Pod: %v - Node: %v", obj.(*corev1.Pod).Name, obj.(*corev1.Pod).Spec.NodeName)
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

				log.Warningf("cannot convert to *appsv1.ReplicaSet: %v", new)
				return
			}
			log.Infof("replicaSet %s Added", replicaSet.Name)
		},
		UpdateFunc: func(old, new interface{}) {
			replicaSet, ok := old.(*appsv1.ReplicaSet)
			if !ok {
				// log.Infof("Replica: %v| Ready replica: %v | Available replica: %v",replicaSet.Status.Replicas,replicaSet.Status.ReadyReplicas, replicaSet.Status.AvailableReplicas)
				log.Warningf("cannot convert oldObj to *appsv1.replicaSet: %v", old)
				return
			}
			_, ok = new.(*appsv1.ReplicaSet)
			if !ok {
				log.Warningf("cannot convert newObj to *appsv1.replicaSet: %v", new)
				return
			}
			log.Infof("replicaSet %s Updated", replicaSet.Name)
		},
		DeleteFunc: func(old interface{}) {
			replicaSet, ok := old.(*appsv1.ReplicaSet)
			if !ok {
				log.Warningf("cannot convert to *appsv1.replicaSet: %v", old)
				return
			}
			log.Infof("replicaSet %s Deleted", replicaSet.Name)
		},
	})
	replicaSetLister := informerFactory.Apps().V1().ReplicaSets().Lister()

	informerFactory.Start(stopCh)
	cache.WaitForCacheSync(stopCh)

	c := routes.InitCache(podInformer, replicaSetLister, customCache)

	http.HandleFunc("/", c.Index)
	http.HandleFunc("/filter", c.Filter)
	s := &http.Server{
		Addr: ":8080",
	}
	if err := s.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

// pod informer = list == replica set check if alerady exist node index
