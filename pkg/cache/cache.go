package cache

import (
	"github.com/go-logr/logr"
	"k8s.io/client-go/informers"
)

type Cache struct {
	Log             logr.Logger
	InformerFactory informers.SharedInformerFactory
}
