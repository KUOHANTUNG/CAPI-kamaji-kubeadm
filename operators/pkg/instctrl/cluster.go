package instctrl

import (
	"context"
	"fmt"

	"github.com/netgroup-polito/CrownLabs/operators/api/v1alpha2"
	clctx "github.com/netgroup-polito/CrownLabs/operators/pkg/context"
	"github.com/netgroup-polito/CrownLabs/operators/pkg/forge"
	"github.com/netgroup-polito/CrownLabs/operators/pkg/utils"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	intstr "k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
	infrav1 "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	bootstrapv1 "sigs.k8s.io/cluster-api/bootstrap/kubeadm/api/v1beta1"
	controlplanev1 "sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
)

// InstanceReconciler enforces the Cluster API environment for a CrownLabs instance
func (r *InstanceReconciler) EnforceClusterEnvironment(ctx context.Context) error {
	fmt.Println("123")
	r.enforceCluster(ctx)
	r.enforceInfra(ctx)
	r.enforceControlPlane(ctx)
	r.enforceMachineDeployment(ctx)
	r.enforceKubevirtMachine(ctx)
	r.enforceBootstrap(ctx)
	return nil
}

// enforceCluster creates or updates the Cluster resource and sets its OwnerRef
func (r *InstanceReconciler) enforceCluster(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	instance := clctx.InstanceFrom(ctx)
	environment := clctx.EnvironmentFrom(ctx)
	cluster := environment.Cluster
	clusternet := cluster.ClusterNet
	controlplane := cluster.ControlPlane
	cl := &capiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-cluster", cluster.Name),
			Namespace: instance.Namespace,
		},
	}
	res, err := ctrl.CreateOrUpdate(ctx, r.Client, cl, func() error {
		// ensure refs exist
		if cl.Spec.InfrastructureRef == nil {
			cl.Spec.InfrastructureRef = &corev1.ObjectReference{}
		}
		if cl.Spec.ControlPlaneRef == nil {
			cl.Spec.ControlPlaneRef = &corev1.ObjectReference{}
		}
		// align infrastructure and controlPlane refs
		cl.Spec.InfrastructureRef.APIVersion = infrav1.GroupVersion.String()
		cl.Spec.InfrastructureRef.Kind = "KubevirtCluster"
		cl.Spec.InfrastructureRef.Name = fmt.Sprintf("%s-infra", cluster.Name)

		cl.Spec.ControlPlaneRef.APIVersion = controlplanev1.GroupVersion.String()
		if controlplane.Provider == v1alpha2.ProviderKubeadm {
			cl.Spec.ControlPlaneRef.Kind = "KubeadmControlPlane"
		} else {
			cl.Spec.ControlPlaneRef.Kind = "KamajiControlPlane"
		}
		cl.Spec.ControlPlaneRef.Name = fmt.Sprintf("%s-control-plane", cluster.Name)

		// align network and endpoint
		cl.Spec.ClusterNetwork = &capiv1.ClusterNetwork{
			Pods:     &capiv1.NetworkRanges{CIDRBlocks: clusternet.Pods.CIDRBlocks},
			Services: &capiv1.NetworkRanges{CIDRBlocks: clusternet.Services.CIDRBlocks},
		}
		cl.Spec.ControlPlaneEndpoint = capiv1.APIEndpoint{
			Host: fmt.Sprintf("%s.%s.svc.cluster.local", cluster.Name, instance.Namespace),
			Port: 6443,
		}

		return ctrl.SetControllerReference(instance, cl, r.Scheme)
	})
	if err != nil {
		log.Error(err, "failed to enforce cluster", "cluster", klog.KObj(cl))
		return err
	}
	log.V(utils.FromResult(res)).Info("Cluster enforced", "Cluster", klog.KObj(cl), "result", res)
	return nil
}

// enforceInfra creates or updates the KubevirtCluster resource and labels it for CAPI
func (r *InstanceReconciler) enforceInfra(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	instance := clctx.InstanceFrom(ctx)
	environment := clctx.EnvironmentFrom(ctx)
	cluster := environment.Cluster
	infra := &infrav1.KubevirtCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-infra", cluster.Name),
			Namespace: instance.Namespace,
		},
	}
	res, err := ctrl.CreateOrUpdate(ctx, r.Client, infra, func() error {
		infra.Spec.ControlPlaneServiceTemplate.Spec.Type = corev1.ServiceType(cluster.ServiceType)
		if infra.Labels == nil {
			infra.Labels = map[string]string{}
		}
		infra.Labels[capiv1.ClusterNameLabel] = fmt.Sprintf("%s-cluster", cluster.Name)
		return nil
	})
	if err != nil {
		log.Error(err, "failed to enforce infrastructure", "infra", klog.KObj(infra))
		return err
	}
	log.V(utils.FromResult(res)).Info("Infrastructure enforced", "infra", klog.KObj(infra), "result", res)
	return nil
}

// enforceControlPlane creates or updates the KubeadmControlPlane resource and labels it
func (r *InstanceReconciler) enforceControlPlane(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	instance := clctx.InstanceFrom(ctx)
	environment := clctx.EnvironmentFrom(ctx)
	cluster := environment.Cluster
	clusternet := cluster.ClusterNet
	controlplane := cluster.ControlPlane
	cp := &controlplanev1.KubeadmControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-control-plane", cluster.Name),
			Namespace: instance.Namespace,
		},
	}
	res, err := ctrl.CreateOrUpdate(ctx, r.Client, cp, func() error {
		cp.Spec.Version = cluster.Version
		cp.Spec.Replicas = pt(int32(controlplane.Replicas))
		// align kubeadm specs
		cp.Spec.KubeadmConfigSpec.ClusterConfiguration = &bootstrapv1.ClusterConfiguration{
			Networking: bootstrapv1.Networking{
				DNSDomain:     fmt.Sprintf("%s.%s.local", instance.Name, instance.Namespace),
				PodSubnet:     clusternet.Pods.CIDRBlocks[0],
				ServiceSubnet: clusternet.Services.CIDRBlocks[0],
			},
		}
		cp.Spec.KubeadmConfigSpec.InitConfiguration = &bootstrapv1.InitConfiguration{
			NodeRegistration: bootstrapv1.NodeRegistrationOptions{CRISocket: "unix:///var/run/containerd/containerd.sock"},
		}
		cp.Spec.KubeadmConfigSpec.ClusterConfiguration.APIServer.CertSANs = controlplane.CertSANs
		cp.Spec.KubeadmConfigSpec.JoinConfiguration = &bootstrapv1.JoinConfiguration{NodeRegistration: bootstrapv1.NodeRegistrationOptions{CRISocket: "unix:///var/run/containerd/containerd.sock"}}
		// infra ref
		cp.Spec.MachineTemplate.InfrastructureRef = corev1.ObjectReference{APIVersion: infrav1.GroupVersion.String(), Kind: "KubevirtMachineTemplate", Name: fmt.Sprintf("%s-control-plane-machine", cluster.Name)}
		if cp.Labels == nil {
			cp.Labels = map[string]string{}
		}
		cp.Labels[capiv1.ClusterNameLabel] = fmt.Sprintf("%s-cluster", cluster.Name)
		return nil
	})
	if err != nil {
		log.Error(err, "failed to enforce controlplane", "cp", klog.KObj(cp))
		return err
	}
	log.V(utils.FromResult(res)).Info("ControlPlane enforced", "cp", klog.KObj(cp), "result", res)
	return nil
}

// enforceMachineDeployment creates or updates the MachineDeployment and labels it
func (r *InstanceReconciler) enforceMachineDeployment(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	instance := clctx.InstanceFrom(ctx)
	environment := clctx.EnvironmentFrom(ctx)
	cluster := environment.Cluster
	machinedeployment := cluster.MachineDeploy
	md := &capiv1.MachineDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-md", cluster.Name), Namespace: instance.Namespace},
	}
	_, err := ctrl.CreateOrUpdate(ctx, r.Client, md, func() error {
		md.Spec.ClusterName = fmt.Sprintf("%s-cluster", cluster.Name)
		md.Spec.Replicas = pt(int32(machinedeployment.Replicas))
		md.Spec.Template.Spec.ClusterName = fmt.Sprintf("%s-cluster", cluster.Name)
		md.Spec.Template.Spec.Bootstrap.ConfigRef = &corev1.ObjectReference{APIVersion: bootstrapv1.GroupVersion.String(), Kind: "KubeadmConfigTemplate", Name: fmt.Sprintf("%s-md-bootstrap", cluster.Name)}
		md.Spec.Template.Spec.InfrastructureRef = corev1.ObjectReference{APIVersion: infrav1.GroupVersion.String(), Kind: "KubevirtMachineTemplate", Name: fmt.Sprintf("%s-md-worker", cluster.Name)}
		md.Spec.Template.Spec.Version = &cluster.Version
		// rolling strategy
		maxSurge := intstr.FromInt(1)
		maxUnavailable := intstr.FromInt(0)
		md.Spec.Strategy = &capiv1.MachineDeploymentStrategy{Type: capiv1.RollingUpdateMachineDeploymentStrategyType, RollingUpdate: &capiv1.MachineRollingUpdateDeployment{MaxSurge: &maxSurge, MaxUnavailable: &maxUnavailable}}
		if md.Labels == nil {
			md.Labels = map[string]string{}
		}
		md.Labels[capiv1.ClusterNameLabel] = fmt.Sprintf("%s-cluster", cluster.Name)
		return nil
	})
	if err != nil {
		log.Error(err, "failed to enforce machinedeployment")
		return err
	}
	return nil
}

// enforceKubevirtMachine creates or updates KubevirtMachineTemplates with RunStrategy and DV mapping
func (r *InstanceReconciler) enforceKubevirtMachine(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	instance := clctx.InstanceFrom(ctx)
	environment := clctx.EnvironmentFrom(ctx)
	cluster := environment.Cluster
	controlplane := cluster.ControlPlane

	// worker template
	wmworker := infrav1.KubevirtMachineTemplate{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-md-worker", cluster.Name), Namespace: instance.Namespace}}
	res, err := ctrl.CreateOrUpdate(ctx, r.Client, &wmworker, func() error {
		if wmworker.CreationTimestamp.IsZero() {
			wmworker.Spec.Template.Spec.BootstrapCheckSpec.CheckStrategy = "ssh"

			vmSpec := forge.VirtualMachineSpec(instance, environment)
			wmworker.Spec.Template.Spec.VirtualMachineTemplate.Spec = vmSpec
		}

		// label
		wmworker.Spec.Template.Spec.VirtualMachineTemplate.Spec.Running = ptr.To(instance.Spec.Running)
		if wmworker.Labels == nil {
			wmworker.Labels = map[string]string{}
		}
		wmworker.Labels[capiv1.ClusterNameLabel] = fmt.Sprintf("%s-cluster", cluster.Name)
		return nil
	})
	if err != nil {
		log.Error(err, "failed to enforce virtualmachine-worker")
		return err
	}
	log.V(utils.FromResult(res)).Info("virtualmachine-worker enforced")

	// control-plane template
	if controlplane.Provider == v1alpha2.ProviderKubeadm {
		wmcp := infrav1.KubevirtMachineTemplate{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-control-plane-machine", cluster.Name), Namespace: instance.Namespace}}
		res, err := ctrl.CreateOrUpdate(ctx, r.Client, &wmcp, func() error {

			if wmcp.CreationTimestamp.IsZero() {
				wmcp.Spec.Template.Spec.BootstrapCheckSpec.CheckStrategy = "ssh"

				vmSpec := forge.VirtualMachineSpec(instance, environment)
				wmcp.Spec.Template.Spec.VirtualMachineTemplate.Spec = vmSpec
			}
			wmcp.Spec.Template.Spec.VirtualMachineTemplate.Spec.Running = ptr.To(instance.Spec.Running)
			if wmcp.Labels == nil {
				wmcp.Labels = map[string]string{}
			}
			wmcp.Labels[capiv1.ClusterNameLabel] = fmt.Sprintf("%s-cluster", cluster.Name)
			return nil
		})
		if err != nil {
			log.Error(err, "failed to enforce virtualmachine-control-plane")
			return err
		}
		log.V(utils.FromResult(res)).Info("virtualmachine-control-plane enforced")
	}
	return nil
}

// enforceBootstrap creates or updates the KubeadmConfigTemplate and labels it
func (r *InstanceReconciler) enforceBootstrap(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	instance := clctx.InstanceFrom(ctx)
	environment := clctx.EnvironmentFrom(ctx)
	cluster := environment.Cluster
	bt := bootstrapv1.KubeadmConfigTemplate{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-md-bootstrap", cluster.Name), Namespace: instance.Namespace}}
	res, err := ctrl.CreateOrUpdate(ctx, r.Client, &bt, func() error {
		if bt.CreationTimestamp.IsZero() {
			bt.Spec.Template.Spec.JoinConfiguration = &bootstrapv1.JoinConfiguration{NodeRegistration: bootstrapv1.NodeRegistrationOptions{KubeletExtraArgs: map[string]string{}}}
		}
		if bt.Labels == nil {
			bt.Labels = map[string]string{}
		}
		bt.Labels[capiv1.ClusterNameLabel] = fmt.Sprintf("%s-cluster", cluster.Name)
		return nil
	})
	if err != nil {
		log.Error(err, "failed to enforce bootstrap")
		return err
	}
	log.V(utils.FromResult(res)).Info("bootstrap enforced")
	return nil
}

// pt is a generic pointer helper
func pt[T any](v T) *T { return &v }
