package main

import (
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Ridecell/k8s-scheduler-extender/pkg/routes"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/rest"

	kubernetes "k8s.io/client-go/kubernetes"
	goClientCache "k8s.io/client-go/tools/cache"
	clientcmd "k8s.io/client-go/tools/clientcmd"
)

func connectToK8s(logger *zap.Logger) *kubernetes.Clientset {
	home, exists := os.LookupEnv("HOME")
	if !exists {
		home = "/root"
	}

	configPath := filepath.Join(home, ".kube", "config")

	config, err := clientcmd.BuildConfigFromFlags("", configPath)
	if err != nil {
		config, err = rest.InClusterConfig()
		if err != nil {
			logger.Error("Failed to create K8s config", zap.String("Error", err.Error()))
		}
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		logger.Error("Failed to create K8s clientset", zap.String("Error", err.Error()))
	}

	return clientset
}

func main() {
	//build custom zap logger
	config := zap.Config{
		Encoding:         "json",
		Level:            zap.NewAtomicLevelAt(zapcore.InfoLevel),
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
		EncoderConfig: zapcore.EncoderConfig{
			LevelKey:    "level",
			EncodeLevel: zapcore.CapitalLevelEncoder,
			TimeKey:     "ts",
			EncodeTime:  zapcore.RFC3339NanoTimeEncoder,
			MessageKey:  "msg",
		},
	}
	log, _ := config.Build()

	//init k8s client
	clientset := connectToK8s(log)

	// setup k8s informer factory
	informerFactory := informers.NewSharedInformerFactory(clientset, 10*time.Minute)
	p := routes.NewPodsPerNode(informerFactory, log)
	stopCh := make(chan struct{})
	informerFactory.Start(stopCh)
	goClientCache.WaitForCacheSync(stopCh)

	http.HandleFunc("/index", routes.Index)
	http.HandleFunc("/api/podspernode/filter", p.PodsPerNodeFilter)
	port, exists := os.LookupEnv("PORT")
	if !exists {
		port = "8086"
	}
	s := &http.Server{
		Addr: ":" + port,
	}

	if err := s.ListenAndServe(); err != nil {
		log.Error("Server error", zap.String("Error", err.Error()))
	}
}
