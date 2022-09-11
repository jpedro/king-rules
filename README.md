# King Rules

[![Go Report Card](https://goreportcard.com/badge/github.com/jpedro/king-rules)](https://goreportcard.com/report/github.com/jpedro/king-rules)

This dynamically attaches a service to an existing ingress' rules.

`king-rules` is short for Kubernetes INGress Rules. I'm kidding. I just
wanna use that Mel Brooks meme!


## Motivation

This came out of a desire to deploy development branches in kubernetes
**without** creating a new ingress *all the time*. Creating it normally
requires time to provision a cloud specific load balancer and that can take
minutes to become active and accept traffic.

Instead we re-use the same ingress and just attach new rules as needed.

We assume that each service will respond to its own subdomain (the `host`
setting in the ingress' rule). Using wildcards at the DNS and LB
certificate levels, one can expose these services in subdomains faster.

I drew inspiration from https://github.com/hxquangnhat/kubernetes-auto-ingress
but that code creates a new ingress for each seervice every time, which is
exactly what we're trying to avoid here.


## Usage

You need to specify these 2 annotations in your service:

```yaml
king-rules/name: dominion
king-rules/host: echo.example.com
```

The ingress `dominion` will get a new rule, with the format:

```yaml
  - http:
      host: echo.example.com
      paths:
      - backend:
          serviceName: {{ service.Name }}
          servicePort: {{ service.Ports[0].Port }}
```

Note that you need to have the **ingress created previously** as this code will
not do that for you. Otherwise, it would [defeat the purpose of this repo](https://github.com/jpedro/king-rules#motivation).
This simplifies this code and you can provision it exactly the way you need it.


## Example

### Deploy the executable and the common ingress

First, deploy this `king-rules` code via a deployment:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: king-rules
spec:
  selector:
    matchLabels:
      app: king-rules
  replicas: 1
  template:
    metadata:
      labels:
        app: king-rules
    spec:
      containers:
      - name: king-rules
        image: jpedrob/king-rules
        imagePullPolicy: Always
        env:
        - name: LOG_LEVEL
          value: debug
        # Required as this info is not passed on to the container
        - name: NAMESPACE
          valueFrom:
            fieldRef:
              fieldPath: metadata.namespace
```

Now you can start tailing the logs for the deployed `king-rules` pod:

```bash
kubectl logs -f $(kubectl get pods -o name -l app=king-rules)
```

Now, create the `common` ingress that will hold the rules that will be
updated as services come and go:

```yaml
apiVersion: extensions/v1beta1
kind: Ingress
metadata:
  name: dominion
  annotations:
    kubernetes.io/ingress.class: "ingress-alb"
    alb.ingress.kubernetes.io/scheme: internal
    alb.ingress.kubernetes.io/certificate-arn: "arn:aws:acm:eu-west-1:xxx:certificate/xxx"
    alb.ingress.kubernetes.io/backend-protocol: HTTP
    alb.ingress.kubernetes.io/healthcheck-path: /
    alb.ingress.kubernetes.io/listen-ports: '[{"HTTP": 80}, {"HTTPS": 443}]'
    alb.ingress.kubernetes.io/actions.ssl-redirect: '{"Type": "redirect", "RedirectConfig": { "Protocol": "HTTPS", "Port": "443", "StatusCode": "HTTP_301"}}'

spec:
  rules:
  - http:
      paths:
      - backend:
          serviceName: ssl-redirect
          servicePort: use-annotation
        path: /*
      - backend:
          serviceName: fallback
          servicePort: 80
```


### Deploy your pod and service

Create a deployment for your service:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: echo
spec:
  selector:
    matchLabels:
      app: echo
  replicas: 1
  template:
    metadata:
      labels:
        app: echo
    spec:
      containers:
      - name: echo
        image: jpedrob/echo:v0.1.0
        imagePullPolicy: Always
        env:
        - name: TEST_SUBDOMAIN
          value: echo.example.com
```

Finally, create the service that uses those 3 annotations:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: echo
  labels:
    app: echo
  annotations:
    king-rules/name: dominion
    king-rules/host: echo.example.com
spec:
  type: NodePort
  selector:
    app: echo
  ports:
  - name: http
    port: 80
    targetPort: http
```

Check that the new service was attached to a new rule:

```bash
$ kubectl get ingress dominion -o yaml
```

## Todos

- [ ] Allow multiple or all namespaces

- [ ] Check king-rules service account permissions

- [ ] Support the `networking.k8s.io/v1` apiGroup, not just
      `extensions/v1beta1`.

- [ ] Support comma-separated hosts in the `king-rules/host`.

- [ ] Support comma-separated paths in the `king-rule/path: /xxx`. The
      usage of this setting combined with the host will create a matrix
      `host x path` of `[]HTTPIngressPath`.

[![Good](/.github/king.jpeg)](https://www.imdb.com/title/tt0082517/)
