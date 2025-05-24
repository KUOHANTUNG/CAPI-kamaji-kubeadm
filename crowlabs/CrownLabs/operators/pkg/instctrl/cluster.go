package instctrl

import (
	"context"
	"fmt"

	clctx "github.com/netgroup-polito/CrownLabs/operators/pkg/context"
	"github.com/netgroup-polito/CrownLabs/operators/pkg/forge"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "kubevirt.io/api/core/v1"
	infrav1 "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	bootstrapv1 "sigs.k8s.io/cluster-api/bootstrap/kubeadm/api/v1beta1"
	controlplanev1 "sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func (r *InstanceReconciler) EnforceClusterEnvironment(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	enviroment := clctx.EnvironmentFrom(ctx)

}

func (r *InstanceReconciler) enforceCluster(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	instance := clctx.InstanceFrom(ctx)
	environment := clctx.EnvironmentFrom(ctx)
	cluster := environment.Cluster
	clusternet := cluster.ClusterNet
	controlplane := cluster.ControlPlane
	cl := &capiv1.Cluster{ObjectMeta: forge.ObjectMeta(instance)}
	_, err := ctrl.CreateOrUpdate(ctx, r.Client, cl, func() error {
		if cl.CreationTimestamp.IsZero() {
			// set the network
			cl.Spec.ClusterNetwork.Pods.CIDRBlocks = clusternet.Pods.CIDRBlocks
			cl.Spec.ClusterNetwork.Services.CIDRBlocks = clusternet.Services.CIDRBlocks
			// set the controlplane
			cl.Spec.ControlPlaneRef.APIVersion = controlplanev1.GroupVersion.String()
			cl.Spec.ControlPlaneRef.Kind = string(controlplane.Provider)
			cl.Spec.ControlPlaneRef.Name = controlplane.Name
			// set the infrastructure
			cl.Spec.InfrastructureRef.APIVersion = infrav1.GroupVersion.String()
			cl.Spec.InfrastructureRef.Kind = "KubevirtCluster"
			cl.Spec.InfrastructureRef.Name = cluster.Name
		}
		return nil
	})
	if err != nil {
		log.Error(err, "failed to enforce cluster")
		return err
	}
	controllerutil.SetControllerReference(instance, cl, r.Scheme)

	return nil
}

func (r *InstanceReconciler) enforceInfra(ctx context.Context) error {

	log := ctrl.LoggerFrom(ctx)
	instance := clctx.InstanceFrom(ctx)
	environment := clctx.EnvironmentFrom(ctx)
	cluster := environment.Cluster
	infra := &infrav1.KubevirtCluster{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-control-plane", instance.Name), Namespace: instance.Namespace}}
	_, err := ctrl.CreateOrUpdate(ctx, r.Client, infra, func() error {
		if infra.CreationTimestamp.IsZero() {
			infra.Spec.ControlPlaneServiceTemplate = infrav1.ControlPlaneServiceTemplate{
				Spec: infrav1.ServiceSpecTemplate{
					Type: corev1.ServiceType(cluster.ServiceType),
				},
			}
		}
		return nil
	})
	if err != nil {
		log.Error(err, "create/update KubevirtCluster")
		return err
	}

	return nil
}

func (r *InstanceReconciler) enforceControlPlane(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	instance := clctx.InstanceFrom(ctx)
	environment := clctx.EnvironmentFrom(ctx)
	cluster := environment.Cluster
	clusternet := cluster.ClusterNet
	controlplane := cluster.ControlPlane
	cp := &controlplanev1.KubeadmControlPlane{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-control-plane", instance.Name), Namespace: instance.Namespace}}
	_, err := ctrl.CreateOrUpdate(ctx, r.Client, cp, func() error {
		cp.Spec.Version = cluster.Version
		cp.Spec.Replicas = pt(int32(controlplane.Replicas))
		cp.Spec.KubeadmConfigSpec = bootstrapv1.KubeadmConfigSpec{
			ClusterConfiguration: &bootstrapv1.ClusterConfiguration{
				Networking: bootstrapv1.Networking{
					DNSDomain:     fmt.Sprintf("%s.%s.local", instance.Name, instance.Namespace),
					PodSubnet:     clusternet.Pods.CIDRBlocks[0],
					ServiceSubnet: clusternet.Services.CIDRBlocks[0],
				},
			},
		}
		cp.Spec.KubeadmConfigSpec.ClusterConfiguration.APIServer.CertSANs = controlplane.CertSANs
		cp.Spec.KubeadmConfigSpec.InitConfiguration = &bootstrapv1.InitConfiguration{NodeRegistration: bootstrapv1.NodeRegistrationOptions{CRISocket: "unix:///var/run/containerd/containerd.sock"}}
		cp.Spec.KubeadmConfigSpec.JoinConfiguration = &bootstrapv1.JoinConfiguration{NodeRegistration: bootstrapv1.NodeRegistrationOptions{CRISocket: "unix:///var/run/containerd/containerd.sock"}}
		cp.Spec.MachineTemplate.InfrastructureRef = corev1.ObjectReference{APIVersion: infrav1.GroupVersion.String(), Kind: "KubevirtMachineTemplate", Name: fmt.Sprintf("%s-control-plane", instance.Name)}
		ctrl.SetControllerReference(instance, cp, r.Scheme)
		return nil
	})
	if err != nil {
		log.Error(err, "create/update KubeadmControlPlane")
		return err
	}
	return nil
}

func (r *InstanceReconciler) enforceMachineDeployment(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	instance := clctx.InstanceFrom(ctx)
	environment := clctx.EnvironmentFrom(ctx)
	cluster := environment.Cluster
	machinedeployment := cluster.MachineDeploy
	md := &capiv1.MachineDeployment{ObjectMeta: metav1.ObjectMeta{Name: machinedeployment.Name, Namespace: instance.Namespace}}
	_, err := ctrl.CreateOrUpdate(ctx, r.Client, md, func() error {
		md.Spec.ClusterName = cluster.Name
		md.Spec.Replicas = pt(int32(machinedeployment.Replicas))
		md.Spec.Template.Spec.Bootstrap.ConfigRef = &corev1.ObjectReference{
			APIVersion: bootstrapv1.GroupVersion.String(), Kind: "KubeadmConfigTemplate",
			Name: machinedeployment.Name,
		}
		md.Spec.Template.Spec.InfrastructureRef = corev1.ObjectReference{APIVersion: infrav1.GroupVersion.String(), Kind: "KubevirtMachineTemplate", Name: machinedeployment.Name}
		md.Spec.Template.Spec.Version = &cluster.Version
		ctrl.SetControllerReference(instance, md, r.Scheme)
		return nil
	})
	if err != nil {
		log.Error(err, "create/update MachineDeployment")
		return err
	}
	return nil
}

func (r *InstanceReconciler) enforcecpkubevirtmachine(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	instance := clctx.InstanceFrom(ctx)
	environment := clctx.EnvironmentFrom(ctx)
	kv := &infrav1.KubevirtMachineTemplate{ObjectMeta: metav1.ObjectMeta{Name: environment.Name, Namespace: instance.Namespace}}
	_, err := ctrl.CreateOrUpdate(ctx, r.Client, kv, func() error {
		if kv.CreationTimestamp.IsZero() {
			kvTemplate := kv.Spec.Template.Spec
			kvTemplate.BootstrapCheckSpec.CheckStrategy = "ssh"
			kvTemplate.VirtualMachineTemplate.ObjectMeta = metav1.ObjectMeta{Namespace: instance.Namespace}
			virtualMachineTemplate := kvTemplate.VirtualMachineTemplate.Spec
			runStrategy := v1.RunStrategyAlways
			virtualMachineTemplate.RunStrategy = &runStrategy
			virtualMachineTemplate.Template.Spec.Domain.CPU.Cores = environment.Resources.CPU
			virtualMachineTemplate.Template.Spec.Domain.Devices.Disks = []v1.Disk{
				{
					Name: "containervolume",
					DiskDevice: v1.DiskDevice{
						Disk: &v1.DiskTarget{
							Bus: "virtio",
						},
					},
				},
			}
			memory := v1.Memory{Guest: &environment.Resources.Memory}
			virtualMachineTemplate.Template.Spec.Domain.Memory = &memory
			t := true
			virtualMachineTemplate.Template.Spec.Domain.Devices.NetworkInterfaceMultiQueue = &t
			strategy := v1.EvictionStrategyExternal
			virtualMachineTemplate.Template.Spec.EvictionStrategy = &strategy
			virtualMachineTemplate.Template.Spec.Volumes = []v1.Volume{
				{
					Name: "containervolume",
					VolumeSource: v1.VolumeSource{
						ContainerDisk: &v1.ContainerDiskSource{
							Image: environment.Image,
						},
					},
				},
			}
			return nil
		}
		return nil
	})
	if err != nil {
		log.Error(err, "create/update kubevirtmachine")
		return err
	}
	return nil
}

func pt[T any](v T) *T { return &v }
