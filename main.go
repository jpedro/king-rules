package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	log "github.com/apex/log"
	"github.com/jpedro/color"

	core "k8s.io/api/core/v1"
	"k8s.io/api/extensions/v1beta1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	client           *kubernetes.Clientset
	namespace        string
	attachedServices = make(map[string]*attachedService)
	logLevels        = map[string]log.Level{
		"debug": log.DebugLevel,
		"info":  log.InfoLevel,
		"warn":  log.WarnLevel,
		"error": log.ErrorLevel,
	}
)

type attachedService struct {
	Namespace   string
	ServiceName string
	IngressName string
	Host        string
	// RuleIndex   int
	// PathIndex   int
}

func (attached *attachedService) String() string {
	return fmt.Sprintf(`
		Namespace: %s
		IngressName: %s
		ServiceName: %s
		Host: %s`,
		attached.Namespace,
		attached.IngressName,
		attached.ServiceName,
		attached.Host)
}

func main() {
	log.Info(color.Paint("pale", "OS %s, Arch %s, CPUs %d, GoVersion %s",
		runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.Version()))

	var err error
	var config *rest.Config

	namespace = os.Getenv("NAMESPACE")
	logLevel := strings.ToLower(os.Getenv("LOG_LEVEL"))
	if logLevel == "" {
		logLevel = "debug"
	}
	log.SetLevel(logLevels[logLevel])
	log.Warnf("Started in namespace %s", color.Yellow(namespace))
	log.Warnf("Started with log level %s", color.Yellow(logLevel))

	// If kube-config is specified, use out-of-cluster
	kubeConfig := flag.String("kube-config", "", "Absolute path to the kubeconfig file")
	flag.Parse()

	if *kubeConfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", *kubeConfig)
	} else {
		// Get config when running inside Kubernetes
		config, err = rest.InClusterConfig()
	}

	if err != nil {
		log.Fatal(err.Error())
		return
	}

	client, err = kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatal(err.Error())
		return
	}

	err = buildAttached()
	if err != nil {
		log.Fatal(err.Error())
		return
	}
	log.Info(color.Yellow("Attached services: %v", attachedServices))

	// Create a watch to listen for create/update/delete events on service. New
	// services will be attached if they specify the annotations.
	getter := client.CoreV1().RESTClient()
	selector := fields.Everything()
	listerWatcher := cache.NewListWatchFromClient(getter, "services", namespace, selector)
	_, controller := cache.NewInformer(
		listerWatcher,
		&core.Service{},
		time.Second*0,
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				service := obj.(*core.Service)
				log.Info(color.Green("Service added %s", service.Name))
				attached := attachedServices[service.Name]
				if attached != nil {
					log.Warn(color.Yellow("  Service %s host %s is attached to ingress %s",
						attached.ServiceName,
						attached.Host,
						attached.IngressName))
				} else {
					attachService(service)
				}
			},

			DeleteFunc: func(obj interface{}) {
				service := obj.(*core.Service)
				log.Warn(color.Yellow("Service deleted %s",
					service.Name))

				attached := attachedServices[service.Name]
				if attached != nil {
					removeService(attached)
				} else {
					log.Debug(color.Gray("  Service %s was not attached",
						service.Name))
				}
			},

			UpdateFunc: func(oldObj, newObj interface{}) {
				oldService := oldObj.(*core.Service)
				newService := newObj.(*core.Service)

				log.Warn(color.Yellow("Service updated %s (%s)",
					oldService.Name,
					newService.Name))

				oldAttached := attachedServices[oldService.Name]
				if oldAttached != nil {
					removeService(oldAttached)
				} else {
					log.Debug(color.Gray("  Service %s was not attached",
						oldService.Name))
				}

				newAttached := attachedServices[newService.Name]
				if newAttached != nil {
					log.Warn(color.Yellow("  Service %s host %s is attached to ingress %s",
						newAttached.ServiceName,
						newAttached.Host,
						newAttached.IngressName))
				} else {
					attachService(newService)
				}

			},
		},
	)

	stop := make(chan struct{})
	go controller.Run(stop)
	for {
		time.Sleep(time.Second)
	}
}

func attachService(service *core.Service) {
	name, host := getNameAndHost(service)
	if name == "" {
		log.Debug(color.Gray("  Will not attach service %s",
			service.Name))
		return
	}

	ingress := getIngress(name, service.Namespace)
	if ingress == nil {
		log.Error(color.Red("Ingress %s not found", name))
		return
	}

	ok := addRule(ingress, host, service)
	if ok {
		attached := attachedService{
			Namespace:   service.Namespace,
			ServiceName: service.Name,
			IngressName: ingress.Name,
			Host:        host,
		}
		attachedServices[service.Name] = &attached
	}

	log.Debug(color.Gray("Attached services %s", attachedServices))
}

func removeService(attached *attachedService) {
	log.Info(color.Yellow("Removing service %s host %s from ingress %s ns: %s",
		attached.ServiceName,
		attached.Host,
		attached.IngressName, attached.Namespace))

	ingress := getIngress(attached.IngressName, attached.Namespace)
	if ingress == nil {
		log.Error(color.Red("Ingress %s not found", attached.IngressName))
		return
	}

	rules := []v1beta1.IngressRule{}
	for i, rule := range ingress.Spec.Rules {
		if attached.Host != rule.Host {
			log.Debug(color.Gray("  Keeping rule %d (host %s vs %s)",
				i, rule.Host, attached.Host))
			rules = append(rules, rule)
			continue
		}

		log.Debug(color.Gray("  Checking rule %d to host %s...", i, rule.Host))
		paths := []v1beta1.HTTPIngressPath{}
		for j, path := range rule.HTTP.Paths {
			serviceName := path.Backend.ServiceName
			log.Debug(color.Gray("    Checking path %d to service %s...", j, serviceName))

			if attached.ServiceName == serviceName {
				log.Debug(color.Gray("    Ignoring path %d to service %s", j, serviceName))
				continue
			}

			log.Debug(color.Gray("    Keeping path %d to service %s", j, serviceName))
			paths = append(paths, path)
		}

		if len(paths) > 0 {
			rule.HTTP.Paths = paths
			rules = append(rules, rule)
		}
	}

	ingress.Spec.Rules = rules
	// log.Debug(color.Gray("New rules %s", &ingress.Spec.Rules))
	updated, err := client.
		ExtensionsV1beta1().
		Ingresses(ingress.Namespace).
		Update(
			context.TODO(),
			ingress,
			v1.UpdateOptions{})

	if err != nil {
		log.Error(color.Red(err.Error()))
		return
	}

	log.Debug(color.Gray("Updated rules count: %d", len(updated.Spec.Rules)))
	log.Warn(color.Yellow("Service %s removed from ingress %s",
		attached.ServiceName, attached.IngressName))

	delete(attachedServices, attached.ServiceName)
	log.Debug(color.Yellow("Attached services %s", attachedServices))
}

func addRule(ingress *v1beta1.Ingress, host string, service *core.Service) bool {
	rule := v1beta1.IngressRule{
		Host: host,
		IngressRuleValue: v1beta1.IngressRuleValue{
			HTTP: &v1beta1.HTTPIngressRuleValue{
				Paths: []v1beta1.HTTPIngressPath{},
			},
		},
	}

	path := v1beta1.HTTPIngressPath{
		Backend: v1beta1.IngressBackend{
			ServiceName: service.Name,
			ServicePort: intstr.IntOrString{
				IntVal: service.Spec.Ports[0].Port,
			},
		},
	}

	// log.Debug(color.Gray("New paths: %v", rule.IngressRuleValue.HTTP.Paths))
	rule.HTTP.Paths = append(rule.HTTP.Paths, path)
	ingress.Spec.Rules = append(ingress.Spec.Rules, rule)
	// log.Debug(color.Gray("New rules: %v", &ingress.Spec.Rules))

	updated, err := client.
		ExtensionsV1beta1().
		Ingresses(service.Namespace).
		Update(
			context.TODO(),
			ingress,
			v1.UpdateOptions{})

	log.Warn(color.Yellow("Adding service %s host %s to ingress %s...", service.Name, host, ingress.Name))

	if err != nil {
		log.Error(color.Red(err.Error()))
		return false
	}

	log.Debug(color.Gray("Updated rules count: %d", len(updated.Spec.Rules)))
	return true
}

func getIngress(name string, namespace string) *v1beta1.Ingress {
	log.Debug(color.Gray("Getting ingress %s in namespace %s...", name, namespace))

	if namespace == "" {
		log.Error(color.Red("Namespace %s is empty", namespace))
		return nil
	}

	ingress, err := client.
		ExtensionsV1beta1().
		Ingresses(namespace).
		Get(context.TODO(), name, v1.GetOptions{})

	if err != nil {
		log.Error(color.Red("Couldn't find ingress %s: %s", name, err.Error()))
		return nil
	}

	log.Debug(color.Gray("Found ingress: %s", ingress.GetName()))

	return ingress
}

func buildAttached() error {
	services, err := client.
		CoreV1().
		Services(namespace).
		List(context.TODO(), v1.ListOptions{})

	if err != nil {
		log.Fatal(color.Red("Error loading services: %s.", err))
		panic(err)
	}

	for _, service := range services.Items {
		log.Info(color.Green("Checking service %s...", service.Name))
		name, host := getNameAndHost(&service)
		if name == "" {
			log.Debug(color.Gray("  Skipping service %s", service.Name))
			continue
		}

		ingress := getIngress(name, service.Namespace)
		if ingress == nil {
			log.Warn(color.Yellow("  Skipping service %s: ingress %s not found",
				service.Name, name))
			continue
		}

		count := countAttachments(ingress, &service)
		if count > 0 {
			log.Warn(color.Yellow("  Service %s is attached to ingress %s in %d places",
				service.Name, name, count))
			attachedServices[service.Name] = &attachedService{
				Namespace:   service.Namespace,
				ServiceName: service.Name,
				IngressName: ingress.Name,
				Host:        host,
			}
		} else {
			log.Warn(color.Yellow("  Service %s is not attached to ingress %s",
				service.Name, name))
		}
	}

	return nil
}

func countAttachments(ingress *v1beta1.Ingress, service *core.Service) int {
	name, host := getNameAndHost(service)
	if name == "" {
		return 0
	}

	count := 0
	for i, rule := range ingress.Spec.Rules {
		log.Debug(color.Gray("  Checking rule %d to host %s...", i, rule.Host))

		if rule.Host != host {
			log.Debug(color.Gray("  Skipping rule %d (host: %s vs %s)",
				i, rule.Host, host))
			continue
		}

		for j, path := range rule.HTTP.Paths {
			serviceName := path.Backend.ServiceName
			log.Debug(color.Gray("    Checking path %d to service %s...", j, serviceName))

			if serviceName == service.Name {
				log.Debug(color.Yellow("    Service found: %s", serviceName))
				count++
			}
		}
	}

	log.Debug(color.Gray("  Service %s in ingress %s: found %d matches",
		service.Name, ingress.Name, count))

	return count
}

func getNameAndHost(service *core.Service) (string, string) {
	name, foundName := service.Annotations["king-rules/name"]
	host, foundHost := service.Annotations["king-rules/host"]
	enab, foundEnab := service.Annotations["ingress-rules/enabled"]

	if foundName {
		log.Debug(color.Gray("  Ingress name: %s", name))
	}

	if foundHost {
		log.Debug(color.Gray("  Ingress host: %s", host))
	}

	enabledBool := true
	if foundEnab && enab != "true" {
		enabledBool = false
	}

	if !foundName || !foundHost || !enabledBool {
		log.Debug(color.Gray("  Failed criteria (enabled: %t, name: %s, host: %s)",
			enabledBool, name, host))
		return "", ""
	}

	log.Debug(color.Gray("  Found annotations name %s host %s", name, host))

	return name, host
}
