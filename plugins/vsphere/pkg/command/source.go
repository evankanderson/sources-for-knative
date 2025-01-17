/*
Copyright 2020 VMware, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package command

import (
	"fmt"
	"net/url"
	"time"

	"github.com/spf13/cobra"
	"github.com/vmware-tanzu/sources-for-knative/pkg/apis/sources/v1alpha1"
	"github.com/vmware-tanzu/sources-for-knative/pkg/vsphere"
	"github.com/vmware-tanzu/sources-for-knative/plugins/vsphere/pkg"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
)

type SourceOptions struct {
	Namespace     string
	Name          string
	Address       string
	SkipTLSVerify bool
	SecretRef     string

	SinkURI        string
	SinkAPIVersion string
	SinkKind       string
	SinkName       string

	CheckpointMaxAge time.Duration
	CheckpointPeriod time.Duration
}

func (so *SourceOptions) AsSinkDestination(namespace string) (*duckv1.Destination, error) {
	apiURL, err := so.sinkURL()
	if err != nil {
		return nil, err
	}
	return &duckv1.Destination{
		Ref: so.sinkReference(namespace),
		URI: apiURL,
	}, nil
}

func (so *SourceOptions) sinkURL() (*apis.URL, error) {
	if so.SinkURI == "" {
		return nil, nil
	}
	address, err := url.Parse(so.SinkURI)
	if err != nil {
		return nil, err
	}
	result := apis.URL(*address)
	return &result, nil
}

func (so *SourceOptions) sinkReference(namespace string) *duckv1.KReference {
	if so.SinkAPIVersion == "" {
		return nil
	}
	return &duckv1.KReference{
		APIVersion: so.SinkAPIVersion,
		Kind:       so.SinkKind,
		Namespace:  namespace,
		Name:       so.SinkName,
	}
}

func NewSourceCommand(clients *pkg.Clients) *cobra.Command {
	options := SourceOptions{}
	result := cobra.Command{
		Use:   "source",
		Short: "Create a vSphere source to react to vSphere events",
		Long:  "Create a vSphere source to react to vSphere events",
		Example: `# Create the source in the default namespace, sending events to the specified sink URI
kn vsphere source --name source --address https://my-vsphere-endpoint.local --skip-tls-verify --secret-ref vsphere-credentials --sink-uri http://where.to.send.stuff
# Create the source in the specified namespace, sending events to the specified service
kn vsphere source --namespace ns --name source --address https://my-vsphere-endpoint.local --skip-tls-verify --secret-ref vsphere-credentials --sink-api-version v1 --sink-kind Service --sink-name the-service-name
# Create the source in the specified namespace, sending events to the specified service with custom checkpoint behavior
kn vsphere source --namespace ns --name source --address https://my-vsphere-endpoint.local --skip-tls-verify --secret-ref vsphere-credentials --sink-api-version v1 --sink-kind Service --sink-name the-service-name --checkpoint-age 1h --checkpoint-period 30s
`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if options.Name == "" {
				return fmt.Errorf("'name' requires a nonempty name provided with the --name option")
			}
			if options.Address == "" {
				return fmt.Errorf("'address' requires a nonempty address provided with the --address option")
			}
			if options.SecretRef == "" {
				return fmt.Errorf("'secret-ref' requires a nonempty secret reference provided with the --secret-ref option")
			}
			sinkCoordinatesAllEmpty := options.SinkAPIVersion == "" && options.SinkKind == "" && options.SinkName == ""
			sinkCoordinatesAllSet := options.SinkAPIVersion != "" && options.SinkKind != "" && options.SinkName != ""
			if options.SinkURI == "" && sinkCoordinatesAllEmpty ||
				(!sinkCoordinatesAllEmpty && !sinkCoordinatesAllSet) {
				return fmt.Errorf("sink requires an URI" +
					"\nand/or a nonempty API version --sink-api-version option," +
					"\nwith a nonempty kind --sink-kind option," +
					"\nand with a nonempty name with the --sink-name")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			namespace, err := clients.GetExplicitOrDefaultNamespace(options.Namespace)
			if err != nil {
				return fmt.Errorf("failed to get namespace: %+v", err)
			}
			address, err := url.Parse(options.Address)
			if err != nil {
				return fmt.Errorf("failed to parse source address: %+v", err)
			}
			sinkDestination, err := options.AsSinkDestination(namespace)
			if err != nil {
				return fmt.Errorf("failed to parse sink address: %+v", err)
			}
			if _, err = clients.VSphereClientSet.
				SourcesV1alpha1().
				VSphereSources(namespace).
				Create(cmd.Context(), newSource(namespace, sinkDestination, address, options), metav1.CreateOptions{}); err != nil {
				return fmt.Errorf("failed to create source: %+v", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Created source")
			return nil
		},
	}
	flags := result.Flags()
	flags.StringVarP(&options.Namespace, "namespace", "n", "", "namespace of the source to create (default namespace if omitted)")
	flags.StringVar(&options.Name, "name", "", "name of the source to create")
	_ = result.MarkFlagRequired("name")
	flags.StringVarP(&options.Address, "address", "a", "", "URL of ESXi or vCenter instance to connect to (same as VC_URL)")
	_ = result.MarkFlagRequired("address")
	flags.BoolVarP(&options.SkipTLSVerify, "skip-tls-verify", "k", false, "disables certificate verification for the source address (same as VC_INSECURE)")
	flags.StringVarP(&options.SecretRef, "secret-ref", "s", "", "reference to the Kubernetes secret for the vSphere credentials needed for the source address")
	_ = result.MarkFlagRequired("secret-ref")
	flags.StringVarP(&options.SinkURI, "sink-uri", "u", "", "sink URI (can be absolute, or relative to the referred sink resource)")
	flags.StringVar(&options.SinkAPIVersion, "sink-api-version", "", "sink API version")
	flags.StringVar(&options.SinkKind, "sink-kind", "", "sink kind")
	flags.StringVar(&options.SinkName, "sink-name", "", "sink name")
	flags.DurationVar(&options.CheckpointMaxAge, "checkpoint-age", vsphere.CheckpointDefaultAge,
		"maximum allowed age for replaying events determined by last successful event in checkpoint")
	flags.DurationVar(&options.CheckpointPeriod, "checkpoint-period", vsphere.CheckpointDefaultPeriod,
		"period between saving checkpoints")
	return &result
}

func newSource(namespace string, sinkDestination *duckv1.Destination, address *url.URL, options SourceOptions) *v1alpha1.VSphereSource {
	return &v1alpha1.VSphereSource{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      options.Name,
		},
		Spec: v1alpha1.VSphereSourceSpec{
			SourceSpec: duckv1.SourceSpec{
				Sink: *sinkDestination,
			},
			VAuthSpec: v1alpha1.VAuthSpec{
				Address:       apis.URL(*address),
				SkipTLSVerify: options.SkipTLSVerify,
				SecretRef: corev1.LocalObjectReference{
					Name: options.SecretRef,
				},
			},
			CheckpointConfig: v1alpha1.VCheckpointSpec{
				// rounding errors are ok here
				MaxAgeSeconds: int64(options.CheckpointMaxAge.Seconds()),
				PeriodSeconds: int64(options.CheckpointPeriod.Seconds()),
			},
		},
	}
}
