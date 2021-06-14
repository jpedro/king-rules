package main

import (
	"context"
	"flag"
	"fmt"
	"os"
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
	logLevel         string
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
	RuleIndex   int
	PathIndex   int
}

func (attached *attachedService) String() string {
	return fmt.Sprintf(`
		IngressName: %s
		ServiceName: %s
		Host: %s
		RuleIndex: %d
		PathIndex: %d`,
		attached.IngressName,
		attached.ServiceName,
		attached.Host,
		attached.RuleIndex,
		attached.PathIndex)
}

func main() {
	var err error
	var config *rest.Config

	namespace = os.Getenv("NAMESPACE")
	logLevel = strings.ToLower(os.Getenv("LOG_LEVEL"))
	if logLevel == "" {
		logLevel = "info"
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
	log.Infof("Already attached services:\n%v", attachedServices)

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
				log.Infof(color.Green("Service added %s", service.Name))
				attached := attachedServices[service.Name]
				if attached != nil {
					log.Warnf(color.Yellow("Service %s is already attached to ingress %s",
						attached.ServiceName,
						attached.IngressName))
				} else {
					attachService(service)
				}
			},

			DeleteFunc: func(obj interface{}) {
				service := obj.(*core.Service)
				log.Warnf(color.Yellow("Service deleted %s",
					service.Name))

				attached := attachedServices[service.Name]
				if attached != nil {
					removeService(attached)
				} else {
					log.Debugf(color.Gray("Service %s was not attached",
						service.Name))
				}
			},

			UpdateFunc: func(oldObj, newObj interface{}) {
				service := newObj.(*core.Service)
				log.Warnf(color.Yellow("Service updated %s",
					service.Name))
			},
		},
	)

	stop := make(chan struct{})
	go controller.Run(stop)
	for {
		time.Sleep(time.Second)
	}
}

func removeService(attached *attachedService) {
	log.Infof(color.Yellow("Removing service %s host %s from ingress %s",
		attached.ServiceName,
		attached.Host,
		attached.IngressName))

	removeRule(attached)
}

func attachService(service *core.Service) {
	// if service.Name != "echo" {
	// 	// log.Debugf("Skipping service %s",
	// 	// 	color.Paint("gray", service.Name))
	// 	return
	// }

	name, host := getNameAndHost(service)
	if name == "" {
		log.Debugf(color.Gray("  Will not attach service %s",
			service.Name))
		return
	}

	ingress := getIngress(name, service.Namespace)
	if ingress == nil {
		log.Errorf(color.Red("Ingress %s not found",
			name))
		return
	}

	ruleIndex, pathIndex := addRule(ingress, host, service)
	if ruleIndex > -1 {
		attached := attachedService{
			Namespace:   service.Namespace,
			ServiceName: service.Name,
			IngressName: ingress.Name,
			Host:        host,
			RuleIndex:   ruleIndex,
			PathIndex:   pathIndex,
		}
		attachedServices[service.Name] = &attached
	}

	log.Debugf(color.Gray("Attached services %s", attachedServices))
}

func removeRule(attached *attachedService) {
	ingress := getIngress(attached.IngressName, attached.Namespace)
	rules := []v1beta1.IngressRule{}
	for i, rule := range ingress.Spec.Rules {
		log.Debugf(color.Gray("  Checking rule %d to host: %s...", i, rule.Host))

		if attached.Host != rule.Host {
			log.Debugf(color.Gray("  Keeping rule %d to host %s", i, rule.Host))
			rules = append(rules, rule)
			continue
		}

		paths := []v1beta1.HTTPIngressPath{}
		for j, path := range rule.HTTP.Paths {
			serviceName := path.Backend.ServiceName
			log.Debugf(color.Gray("    Checking path %d to service %s", j, serviceName))

			if attached.ServiceName == serviceName {
				log.Debugf(color.Gray("    Ignoring path %d to service %s", j, serviceName))
				continue
			}

			log.Debugf(color.Gray("    Appending path %d to service %s", j, serviceName))
			paths = append(paths, path)
		}

		if len(paths) > 0 {
			rule.HTTP.Paths = paths
			rules = append(rules, rule)
		}
	}

	ingress.Spec.Rules = rules
	log.Debugf(color.Gray("New rules %s", &ingress.Spec.Rules))

	updated, err := client.
		ExtensionsV1beta1().
		Ingresses(ingress.Namespace).
		Update(
			context.TODO(),
			ingress,
			v1.UpdateOptions{})

	if err != nil {
		log.Errorf(color.Red(err.Error()))
		return
	}

	log.Debugf(color.Gray("Updated ingress %s", updated))
	log.Warnf(color.Yellow("Service %s removed from ingress %s", attached.ServiceName, attached.IngressName))

	delete(attachedServices, attached.ServiceName)
	log.Debugf(color.Gray("Attached services %s", attachedServices))
}

func addRule(ingress *v1beta1.Ingress, host string, service *core.Service) (int, int) {
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

	log.Debugf(color.Gray("New paths: %v", rule.IngressRuleValue.HTTP.Paths))
	rule.HTTP.Paths = append(rule.HTTP.Paths, path)
	ingress.Spec.Rules = append(ingress.Spec.Rules, rule)
	log.Debugf(color.Gray("New rules: %v", &ingress.Spec.Rules))

	updated, err := client.
		ExtensionsV1beta1().
		Ingresses(service.Namespace).
		Update(
			context.TODO(),
			ingress,
			v1.UpdateOptions{})

	log.Warnf(color.Yellow("Adding service %s host %s to ingress %s...", service.Name, host, ingress.Name))

	if err != nil {
		log.Errorf(color.Red(err.Error()))
		return -1, -1
	}

	log.Debugf(color.Gray("Updated: %v", updated))
	return getRulePathIndex(updated, service)
}

func getIngress(name string, namespace string) *v1beta1.Ingress {
	log.Debugf(color.Gray("Getting ingress %s in namespace %s...", name, namespace))
	ingress, err := client.
		ExtensionsV1beta1().
		Ingresses(namespace).
		Get(context.TODO(), name, v1.GetOptions{})

	if err != nil {
		log.Errorf(color.Red("Couldn't find ingress %s: %s", name, err.Error()))
		return nil
	}

	log.Debugf(color.Gray("Found ingress: %s", ingress.GetName()))

	return ingress
}

func buildAttached() error {
	services, err := client.
		CoreV1().
		Services(namespace).
		List(context.TODO(), v1.ListOptions{})

	if err != nil {
		log.Fatalf(color.Red("Error loading services: %s.", err))
		panic(err)
	}

	for _, service := range services.Items {
		log.Infof(color.Green("Checking service %s...", service.Name))
		name, host := getNameAndHost(&service)
		if name == "" {
			log.Debugf(color.Gray("Skipping service %s: no ingress name", service.Name))
			continue
		}

		ingress := getIngress(name, service.Namespace)
		if ingress == nil {
			log.Warnf(color.Gray("Skipping service %s: ingress %s not found", service.Name, name))
			continue
		}

		ruleIndex, pathIndex := getRulePathIndex(ingress, &service)
		if ruleIndex > -1 {
			log.Warnf(color.Yellow("Marking service %s as attached to ingress %s", service.Name, name))
			attachedServices[service.Name] = &attachedService{
				ServiceName: service.Name,
				IngressName: ingress.Name,
				Host:        host,
				RuleIndex:   ruleIndex,
				PathIndex:   pathIndex,
			}
		} else {
			log.Warnf(color.Yellow("Service %s not attached to ingress %s", service.Name, name))
		}
	}

	return nil
}

func getRulePathIndex(ingress *v1beta1.Ingress, service *core.Service) (int, int) {

	name, host := getNameAndHost(service)
	if name == "" {
		return -1, -1
	}

	log.Debugf(color.Gray("Found annotations name: %s, host: %s", name, host))

	for i, rule := range ingress.Spec.Rules {
		log.Debugf(color.Gray("- Checking ingress rule %d, host: %s...", i, rule.Host))

		for j, path := range rule.HTTP.Paths {
			log.Debugf(color.Gray("  - Checking ingress path %d...", j))
			log.Debugf(color.Gray("    Backend service: %s", path.Backend.ServiceName))

			if path.Backend.ServiceName == service.Name && rule.Host == host {
				log.Debugf(color.Yellow("    Service match: %s", service.Name))
				return i, j
			}
		}
	}

	log.Warnf(color.Yellow("Service %s not found in ingress %s rules", service.Name, ingress.Name))

	return -1, -1
}

func getNameAndHost(service *core.Service) (string, string) {
	name, foundName := service.Annotations["king-rules/name"]
	host, foundHost := service.Annotations["king-rules/host"]
	enab, foundEnab := service.Annotations["ingress-rules/enabled"]

	if foundName {
		log.Debugf(color.Gray("  Ingress name: %s", name))
	}

	if foundHost {
		log.Debugf(color.Gray("  Ingress host: %s", host))
	}

	enabledBool := true
	if foundEnab && enab != "true" {
		enabledBool = false
	}

	if !foundName || !foundHost || !enabledBool {
		log.Debugf(color.Gray("  Failed criteria (enabled: %s, name: %s, host: %s)", enab, name, host))
		return "", ""
	}

	return name, host
}
