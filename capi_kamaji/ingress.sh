#!/usr/bin/env bash

echo "Setting the Nginx ingress"
    helm upgrade --install ingress-nginx-kamaji ingress-nginx/ingress-nginx \
        --namespace default \
        --create-namespace \
        --set controller.extraArgs.enable-ssl-passthrough="" \
        --set controller.extraArgs.tcp-services-configmap=default/tcp-services \
        --set controller.service.type=NodePort \
        --set controller.service.nodePorts.https=30444 \
        --set controller.ingressClassResource.name=kamaji-nginx \
        --set controller.ingressClass=kamaji-nginx
    kubectl apply -f /home/worker/Desktop/cloudprog2025/capi_kamaji/ingress_config.yaml
    kubectl patch svc ingress-nginx-kamaji-controller \
            -n default \
            --type='json' \
            -p='[
                {
                "op": "replace",
                "path": "/spec/ports/1/name",
                "value": "k8s-api"
                },
                {
                "op": "replace",
                "path": "/spec/ports/1/port",
                "value": 7443
                },
                {
                "op": "replace",
                "path": "/spec/ports/1/targetPort",
                "value": 7443
                }
            ]'
    clusterctl get kubeconfig capi-kamaji > capi-kamaji.kubeconfig
    yq -i '.clusters[0].cluster.server = "https://controlplane.com:30444"' ./capi-kamaji.kubeconfig