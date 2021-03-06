package manifests

import (
	"encoding/base64"
	"path/filepath"

	"github.com/ghodss/yaml"
	"github.com/pkg/errors"

	"github.com/openshift/installer/pkg/asset"
	"github.com/openshift/installer/pkg/asset/installconfig"
	"github.com/openshift/installer/pkg/asset/machines"
	content "github.com/openshift/installer/pkg/asset/manifests/content/tectonic"
	"github.com/openshift/installer/pkg/asset/tls"
)

const (
	tectonicManifestDir = "tectonic"
)

var (
	tectonicConfigPath = filepath.Join(tectonicManifestDir, "00_cluster-config.yaml")

	_ asset.WritableAsset = (*Tectonic)(nil)
)

// Tectonic generates the dependent resource manifests for tectonic (as against bootkube)
type Tectonic struct {
	TectonicConfig *configurationObject
	FileList       []*asset.File
}

// Name returns a human friendly name for the operator
func (t *Tectonic) Name() string {
	return "Tectonic Manifests"
}

// Dependencies returns all of the dependencies directly needed by the
// Tectonic asset
func (t *Tectonic) Dependencies() []asset.Asset {
	return []asset.Asset{
		&installconfig.InstallConfig{},
		&tls.IngressCertKey{},
		&tls.KubeCA{},
		&machines.ClusterK8sIO{},
		&machines.Worker{},
		&machines.Master{},
		&kubeAddonOperator{},
	}
}

// Generate generates the respective operator config.yml files
func (t *Tectonic) Generate(dependencies asset.Parents) error {
	installConfig := &installconfig.InstallConfig{}
	clusterk8sio := &machines.ClusterK8sIO{}
	worker := &machines.Worker{}
	master := &machines.Master{}
	addon := &kubeAddonOperator{}
	dependencies.Get(installConfig, clusterk8sio, worker, master, addon)

	templateData := &tectonicTemplateData{
		KubeAddonOperatorImage: "quay.io/coreos/kube-addon-operator-dev:375423a332f2c12b79438fc6a6da6e448e28ec0f",
		PullSecret:             base64.StdEncoding.EncodeToString([]byte(installConfig.Config.PullSecret)),
	}

	assetData := map[string][]byte{
		"99_binding-discovery.yaml":                              []byte(content.BindingDiscovery),
		"99_kube-addon-00-appversion.yaml":                       []byte(content.AppVersionKubeAddon),
		"99_kube-addon-01-operator.yaml":                         applyTemplateData(content.KubeAddonOperator, templateData),
		"99_openshift-cluster-api_cluster.yaml":                  clusterk8sio.Raw,
		"99_openshift-cluster-api_master-machines.yaml":          master.MachinesRaw,
		"99_openshift-cluster-api_master-user-data-secrets.yaml": master.UserDataSecretsRaw,
		"99_openshift-cluster-api_worker-machineset.yaml":        worker.MachineSetRaw,
		"99_openshift-cluster-api_worker-user-data-secret.yaml":  worker.UserDataSecretRaw,
		"99_role-admin.yaml":                                     []byte(content.RoleAdmin),
		"99_role-user.yaml":                                      []byte(content.RoleUser),
		"99_tectonic-system-00-binding-admin.yaml":               []byte(content.BindingAdmin),
		"99_tectonic-system-02-pull.json":                        applyTemplateData(content.PullTectonicSystem, templateData),
	}

	// addon goes to openshift system
	t.TectonicConfig = configMap("tectonic-system", "cluster-config-v1", genericData{
		"addon-config": string(addon.Files()[0].Data),
	})
	tectonicConfigData, err := yaml.Marshal(t.TectonicConfig)
	if err != nil {
		return errors.Wrap(err, "failed to create tectonic-system/cluster-config-v1 configmap")
	}

	t.FileList = []*asset.File{
		{
			Filename: tectonicConfigPath,
			Data:     tectonicConfigData,
		},
	}
	for name, data := range assetData {
		t.FileList = append(t.FileList, &asset.File{
			Filename: filepath.Join(tectonicManifestDir, name),
			Data:     data,
		})
	}

	return nil
}

// Files returns the files generated by the asset.
func (t *Tectonic) Files() []*asset.File {
	return t.FileList
}

// Load returns the tectonic asset from disk.
func (t *Tectonic) Load(f asset.FileFetcher) (bool, error) {
	fileList, err := f.FetchByPattern(filepath.Join(tectonicManifestDir, "*"))
	if err != nil {
		return false, err
	}
	if len(fileList) == 0 {
		return false, nil
	}

	tectonicConfig := &configurationObject{}
	var found bool
	for _, file := range fileList {
		if file.Filename == tectonicConfigPath {
			if err := yaml.Unmarshal(file.Data, tectonicConfig); err != nil {
				return false, errors.Wrapf(err, "failed to unmarshal 00_cluster-config.yaml")
			}
			found = true
		}
	}

	if !found {
		return false, nil
	}

	t.FileList, t.TectonicConfig = fileList, tectonicConfig
	return true, nil
}
