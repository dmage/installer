package cluster

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/openshift/installer/pkg/asset"
	"github.com/openshift/installer/pkg/asset/installconfig"
	"github.com/openshift/installer/pkg/asset/kubeconfig"
	"github.com/openshift/installer/pkg/terraform"
	"github.com/openshift/installer/pkg/types"
)

const (
	// metadataFileName is name of the file where clustermetadata is stored.
	metadataFileName = "metadata.json"
)

// Cluster uses the terraform executable to launch a cluster
// with the given terraform tfvar and generated templates.
type Cluster struct {
	FileList []*asset.File
}

var _ asset.WritableAsset = (*Cluster)(nil)

// Name returns the human-friendly name of the asset.
func (c *Cluster) Name() string {
	return "Cluster"
}

// Dependencies returns the direct dependency for launching
// the cluster.
func (c *Cluster) Dependencies() []asset.Asset {
	return []asset.Asset{
		&installconfig.InstallConfig{},
		&TerraformVariables{},
		&kubeconfig.Admin{},
	}
}

// Generate launches the cluster and generates the terraform state file on disk.
func (c *Cluster) Generate(parents asset.Parents) (err error) {
	installConfig := &installconfig.InstallConfig{}
	terraformVariables := &TerraformVariables{}
	adminKubeconfig := &kubeconfig.Admin{}
	parents.Get(installConfig, terraformVariables, adminKubeconfig)

	// Copy the terraform.tfvars to a temp directory where the terraform will be invoked within.
	tmpDir, err := ioutil.TempDir("", "openshift-install-")
	if err != nil {
		return errors.Wrap(err, "failed to create temp dir for terraform execution")
	}
	defer os.RemoveAll(tmpDir)

	terraformVariablesFile := terraformVariables.Files()[0]
	if err := ioutil.WriteFile(filepath.Join(tmpDir, terraformVariablesFile.Filename), terraformVariablesFile.Data, 0600); err != nil {
		return errors.Wrap(err, "failed to write terraform.tfvars file")
	}

	metadata := &types.ClusterMetadata{
		ClusterName: installConfig.Config.ObjectMeta.Name,
	}

	defer func() {
		if data, err2 := json.Marshal(metadata); err2 == nil {
			c.FileList = append(c.FileList, &asset.File{
				Filename: metadataFileName,
				Data:     data,
			})
		} else {
			err2 = errors.Wrap(err2, "failed to Marshal ClusterMetadata")
			if err == nil {
				err = err2
			} else {
				logrus.Error(err2)
			}
		}
		// serialize metadata and stuff it into c.FileList
	}()

	switch {
	case installConfig.Config.Platform.AWS != nil:
		metadata.ClusterPlatformMetadata.AWS = &types.ClusterAWSPlatformMetadata{
			Region: installConfig.Config.Platform.AWS.Region,
			Identifier: []map[string]string{
				{
					"tectonicClusterID": installConfig.Config.ClusterID,
				},
				{
					fmt.Sprintf("kubernetes.io/cluster/%s", installConfig.Config.ObjectMeta.Name): "owned",
				},
			},
		}
	case installConfig.Config.Platform.OpenStack != nil:
		metadata.ClusterPlatformMetadata.OpenStack = &types.ClusterOpenStackPlatformMetadata{
			Region: installConfig.Config.Platform.OpenStack.Region,
			Identifier: map[string]string{
				"tectonicClusterID": installConfig.Config.ClusterID,
			},
		}
	case installConfig.Config.Platform.Libvirt != nil:
		metadata.ClusterPlatformMetadata.Libvirt = &types.ClusterLibvirtPlatformMetadata{
			URI: installConfig.Config.Platform.Libvirt.URI,
		}
	default:
		return fmt.Errorf("no known platform")
	}

	logrus.Infof("Using Terraform to create cluster...")
	stateFile, err := terraform.Apply(tmpDir, installConfig.Config.Platform.Name())
	if err != nil {
		err = errors.Wrap(err, "failed to run terraform")
	}

	data, err2 := ioutil.ReadFile(stateFile)
	if err2 == nil {
		c.FileList = append(c.FileList, &asset.File{
			Filename: terraform.StateFileName,
			Data:     data,
		})
	} else {
		if err == nil {
			err = err2
		} else {
			logrus.Errorf("Failed to read tfstate: %v", err2)
		}
	}

	// TODO(yifan): Use the kubeconfig to verify the cluster is up.
	return err
}

// Files returns the FileList generated by the asset.
func (c *Cluster) Files() []*asset.File {
	return c.FileList
}

// Load returns error if the tfstate file is already on-disk, because we want to
// prevent user from accidentally re-launching the cluster.
func (c *Cluster) Load(f asset.FileFetcher) (found bool, err error) {
	_, err = f.FetchByName(terraform.StateFileName)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	return true, fmt.Errorf("%q already exisits.  There may already be a running cluster", terraform.StateFileName)
}

// LoadMetadata loads the cluster metadata from an asset directory.
func LoadMetadata(dir string) (cmetadata *types.ClusterMetadata, err error) {
	raw, err := ioutil.ReadFile(filepath.Join(dir, metadataFileName))
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read %s file", metadataFileName)
	}

	if err = json.Unmarshal(raw, &cmetadata); err != nil {
		return nil, errors.Wrapf(err, "failed to Unmarshal data from %s file to types.ClusterMetadata", metadataFileName)
	}

	return cmetadata, err
}
