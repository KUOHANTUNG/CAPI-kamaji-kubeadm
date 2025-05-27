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
	"k8s.io/klog/v2"
	virtv1 "kubevirt.io/api/core/v1"
	infrav1 "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	bootstrapv1 "sigs.k8s.io/cluster-api/bootstrap/kubeadm/api/v1beta1"
	controlplanev1 "sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *InstanceReconciler) EnforceClusterEnvironment(ctx context.Context) error {

	r.enforceCluster(ctx)
	r.enforceInfra(ctx)
	r.enforceControlPlane(ctx)
	r.enforceMachineDeployment(ctx)
	r.enforcekubevirtmachine(ctx)
	r.enforcebootstrap(ctx)
	return nil
}

func (r *InstanceReconciler) enforceCluster(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	instance := clctx.InstanceFrom(ctx)
	environment := clctx.EnvironmentFrom(ctx)
	cluster := environment.Cluster
	clusternet := cluster.ClusterNet
	controlplane := cluster.ControlPlane
	cl := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-cluster", cluster.Name), Namespace: instance.Namespace}}
	res, err := ctrl.CreateOrUpdate(ctx, r.Client, cl, func() error {
		if cl.CreationTimestamp.IsZero() {
			if cl.Spec.ClusterNetwork == nil {
				cl.Spec.ClusterNetwork = &capiv1.ClusterNetwork{}
			}
			if cl.Spec.ControlPlaneRef == nil {
				cl.Spec.ControlPlaneRef = &corev1.ObjectReference{}
			}
			if cl.Spec.InfrastructureRef == nil {
				cl.Spec.InfrastructureRef = &corev1.ObjectReference{}
			}
			// set the network
			cl.Spec.ClusterNetwork.Pods = &capiv1.NetworkRanges{CIDRBlocks: clusternet.Pods.CIDRBlocks}
			cl.Spec.ClusterNetwork.Services = &capiv1.NetworkRanges{CIDRBlocks: clusternet.Pods.CIDRBlocks}
			// set the controlplane
			cl.Spec.ControlPlaneRef.APIVersion = controlplanev1.GroupVersion.String()
			cl.Spec.ControlPlaneRef.Kind = fmt.Sprintf("%sControlPlane", controlplane.Provider)
			cl.Spec.ControlPlaneRef.Name = fmt.Sprintf("%s-control-plane", cluster.Name)
			// set the infrastructure
			cl.Spec.InfrastructureRef.APIVersion = infrav1.GroupVersion.String()
			cl.Spec.InfrastructureRef.Kind = "KubevirtCluster"
			cl.Spec.InfrastructureRef.Name = fmt.Sprintf("%s-infra", cluster.Name)
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

func (r *InstanceReconciler) enforceInfra(ctx context.Context) error {

	log := ctrl.LoggerFrom(ctx)
	instance := clctx.InstanceFrom(ctx)
	environment := clctx.EnvironmentFrom(ctx)
	cluster := environment.Cluster
	infra := &infrav1.KubevirtCluster{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-infra", cluster.Name), Namespace: instance.Namespace}}
	res, err := ctrl.CreateOrUpdate(ctx, r.Client, infra, func() error {
		if infra.CreationTimestamp.IsZero() {
			infra.Spec.ControlPlaneServiceTemplate = infrav1.ControlPlaneServiceTemplate{
				Spec: infrav1.ServiceSpecTemplate{
					Type: corev1.ServiceType(cluster.ServiceType),
				},
			}
		}
		return ctrl.SetControllerReference(instance, infra, r.Scheme)
	})
	if err != nil {
		log.Error(err, "failed to enforce infrastructure", "infrastructure", klog.KObj(infra))
		return err
	}
	log.V(utils.FromResult(res)).Info("Infrastructure enforced", "Infrastructure", klog.KObj(infra), "result", res)

	return nil
}

func (r *InstanceReconciler) enforceControlPlane(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	instance := clctx.InstanceFrom(ctx)
	environment := clctx.EnvironmentFrom(ctx)
	cluster := environment.Cluster
	clusternet := cluster.ClusterNet
	controlplane := cluster.ControlPlane
	cp := &controlplanev1.KubeadmControlPlane{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-control-plane", cluster.Name), Namespace: instance.Namespace}}
	res, err := ctrl.CreateOrUpdate(ctx, r.Client, cp, func() error {
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
		cp.Spec.MachineTemplate.InfrastructureRef = corev1.ObjectReference{APIVersion: infrav1.GroupVersion.String(), Kind: "KubevirtMachineTemplate", Name: fmt.Sprintf("%s-control-plane-machine", cluster.Name)}
		return ctrl.SetControllerReference(instance, cp, r.Scheme)
	})
	if err != nil {
		log.Error(err, "failed to enforce controlplane", "controlplane", klog.KObj(cp))
		return err
	}
	log.V(utils.FromResult(res)).Info("ControlPlane enforced", "ControlPlane", klog.KObj(cp), "result", res)
	return nil
}

func (r *InstanceReconciler) enforceMachineDeployment(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	instance := clctx.InstanceFrom(ctx)
	environment := clctx.EnvironmentFrom(ctx)
	cluster := environment.Cluster
	machinedeployment := cluster.MachineDeploy
	md := &capiv1.MachineDeployment{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-md", cluster.Name), Namespace: instance.Namespace}}
	_, err := ctrl.CreateOrUpdate(ctx, r.Client, md, func() error {
		md.Spec.ClusterName = fmt.Sprintf("%s-cluster", cluster.Name)
		md.Spec.Template.Spec.ClusterName = fmt.Sprintf("%s-cluster", cluster.Name)
		md.Spec.Replicas = pt(int32(machinedeployment.Replicas))
		md.Spec.Template.Spec.Bootstrap.ConfigRef = &corev1.ObjectReference{
			APIVersion: bootstrapv1.GroupVersion.String(), Kind: "KubeadmConfigTemplate",
			Name: fmt.Sprintf("%s-md-bootstrap", cluster.Name),
		}
		md.Spec.Template.Spec.InfrastructureRef = corev1.ObjectReference{APIVersion: infrav1.GroupVersion.String(), Kind: "KubevirtMachineTemplate", Name: fmt.Sprintf("%s-md-worker", cluster.Name)}
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

func (r *InstanceReconciler) enforcekubevirtmachine(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	instance := clctx.InstanceFrom(ctx)
	environment := clctx.EnvironmentFrom(ctx)
	cluster := environment.Cluster
	controlplane := cluster.ControlPlane
	wmworker := infrav1.KubevirtMachineTemplate{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-md-worker", cluster.Name), Namespace: instance.Namespace}}
	res, err := ctrl.CreateOrUpdate(ctx, r.Client, &wmworker, func() error {
		if wmworker.CreationTimestamp.IsZero() {
			wmworker.Spec.Template.Spec.BootstrapCheckSpec.CheckStrategy = "ssh"
			vmSpec := forge.VirtualMachineSpec(instance, environment)
			wmworker.Spec.Template.Spec.VirtualMachineTemplate.Spec = vmSpec
		}
		return ctrl.SetControllerReference(instance, &wmworker, r.Scheme)
	})
	if err != nil {
		log.Error(err, "failed to enforce virtualmachine-worker", "virtualmachine-worker", klog.KObj(&wmworker))
		return err
	}
	log.V(utils.FromResult(res)).Info("virtualmachine-worker enforced", "virtualmachine-worker", klog.KObj(&wmworker), "result", res)
	vm := virtv1.VirtualMachine{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-md-worker", cluster.Name), Namespace: instance.Namespace}}
	if err = r.Get(ctx, client.ObjectKeyFromObject(&vm), &vm); client.IgnoreNotFound(err) != nil {
		log.Error(err, "failed to retrieve virtualmachine", "virtualmachine", klog.KObj(&wmworker))
		return err
	} else if err != nil {
		klog.Infof("VM %s doesn't exist", instance.Name)
	}
	vmi := virtv1.VirtualMachineInstance{ObjectMeta: forge.ObjectMeta(instance)}
	if err = r.Get(ctx, client.ObjectKeyFromObject(&vmi), &vmi); client.IgnoreNotFound(err) != nil {
		log.Error(err, "failed to retrieve virtualmachineinstance", "virtualmachineinstance", klog.KObj(&vm))
		return err
	} else if err != nil {
		klog.Infof("VMI %s doesn't exist", instance.Name)
	}
	phase := r.RetrievePhaseFromVM(&vm, &vmi)

	if phase != instance.Status.Phase {
		log.Info("phase changed", "virtualmachine", klog.KObj(&vm),
			"previous", string(instance.Status.Phase), "current", string(phase))
		instance.Status.Phase = phase
	}

	// controlplane part
	if controlplane.Provider == v1alpha2.ProviderKubeadm {
		wmcp := infrav1.KubevirtMachineTemplate{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-control-plane-machine", cluster.Name), Namespace: instance.Namespace}}
		res, err := ctrl.CreateOrUpdate(ctx, r.Client, &wmcp, func() error {
			if wmcp.CreationTimestamp.IsZero() {
				wmcp.Spec.Template.Spec.BootstrapCheckSpec.CheckStrategy = "ssh"
				vmSpec := forge.VirtualMachineSpec(instance, environment)
				wmcp.Spec.Template.Spec.VirtualMachineTemplate.Spec = vmSpec
			}
			return ctrl.SetControllerReference(instance, &wmcp, r.Scheme)
		})
		if err != nil {
			log.Error(err, "failed to enforce virtualmachine-control-plane", "virtualmachine-control-plane", klog.KObj(&wmcp))
			return err
		}
		log.V(utils.FromResult(res)).Info("virtualmachine-control-plane enforced", "virtualmachine-control-plane", klog.KObj(&wmcp), "result", res)
		vmcp := virtv1.VirtualMachine{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-md-worker", cluster.Name), Namespace: instance.Namespace}}
		if err = r.Get(ctx, client.ObjectKeyFromObject(&vmcp), &vmcp); client.IgnoreNotFound(err) != nil {
			log.Error(err, "failed to retrieve virtualmachine", "virtualmachine", klog.KObj(&wmcp))
			return err
		} else if err != nil {
			klog.Infof("VM %s doesn't exist", instance.Name)
		}
		vmicp := virtv1.VirtualMachineInstance{ObjectMeta: forge.ObjectMeta(instance)}
		if err = r.Get(ctx, client.ObjectKeyFromObject(&vmicp), &vmicp); client.IgnoreNotFound(err) != nil {
			log.Error(err, "failed to retrieve virtualmachineinstance", "virtualmachineinstance", klog.KObj(&vmcp))
			return err
		} else if err != nil {
			klog.Infof("VMI %s doesn't exist", instance.Name)
		}
		phasecp := r.RetrievePhaseFromVM(&vmcp, &vmicp)

		if phasecp != instance.Status.Phase {
			log.Info("phase changed", "virtualmachine", klog.KObj(&vmcp),
				"previous", string(instance.Status.Phase), "current", string(phasecp))
			instance.Status.Phase = phasecp
		}
	}
	return nil
}

func (r *InstanceReconciler) enforcebootstrap(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	instance := clctx.InstanceFrom(ctx)
	environment := clctx.EnvironmentFrom(ctx)
	cluster := environment.Cluster
	bt := bootstrapv1.KubeadmConfigTemplate{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-md-bootstrap", cluster.Name), Namespace: instance.Namespace}}
	res, err := ctrl.CreateOrUpdate(ctx, r.Client, &bt, func() error {
		if bt.CreationTimestamp.IsZero() {
			bt.Spec.Template.Spec.JoinConfiguration = &bootstrapv1.JoinConfiguration{
				NodeRegistration: bootstrapv1.NodeRegistrationOptions{
					KubeletExtraArgs: map[string]string{},
				},
			}
		}
		return ctrl.SetControllerReference(instance, &bt, r.Scheme)
	})
	if err != nil {
		log.Error(err, "failed to enforce bootstrap", "bootstrap", klog.KObj(&bt))
		return err
	}
	log.V(utils.FromResult(res)).Info("bootstrap enforced", "bootstrap", klog.KObj(&bt), "result", res)
	return nil
}

func pt[T any](v T) *T { return &v }
