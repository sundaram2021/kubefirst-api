/*
Copyright (C) 2021-2023, Kubefirst

This program is licensed under MIT.
See the LICENSE file for more details.
*/
package controller

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/kubefirst/kubefirst-api/internal/types"
	"github.com/kubefirst/runtime/pkg/civo"
	"github.com/kubefirst/runtime/pkg/digitalocean"
	"github.com/kubefirst/runtime/pkg/vultr"
	log "github.com/sirupsen/logrus"
)

var DigitaloceanStateStoreBucketName, VultrStateStoreBucketHostname string

// StateStoreCredentials
func (clctrl *ClusterController) StateStoreCredentials() error {
	cl, err := clctrl.MdbCl.GetCluster(clctrl.ClusterName)
	if err != nil {
		return err
	}

	var stateStoreData types.StateStoreCredentials

	if !cl.StateStoreCredsCheck {
		switch clctrl.CloudProvider {
		case "aws":
			kubefirstStateStoreBucket, err := clctrl.AwsClient.CreateBucket(clctrl.KubefirstStateStoreBucketName)
			if err != nil {
				return err
			}

			kubefirstArtifactsBucket, err := clctrl.AwsClient.CreateBucket(clctrl.KubefirstArtifactsBucketName)
			if err != nil {
				return err
			}

			stateStoreData = types.StateStoreCredentials{
				AccessKeyID:     clctrl.AwsAccessKeyID,
				SecretAccessKey: clctrl.AwsSecretAccessKey,
			}

			err = clctrl.MdbCl.UpdateCluster(clctrl.ClusterName, "state_store_details", types.StateStoreDetails{
				AWSStateStoreBucket: strings.ReplaceAll(*kubefirstStateStoreBucket.Location, "/", ""),
				AWSArtifactsBucket:  strings.ReplaceAll(*kubefirstArtifactsBucket.Location, "/", ""),
			})
			if err != nil {
				return err
			}
		case "civo":
			creds, err := civo.GetAccessCredentials(clctrl.KubefirstStateStoreBucketName, clctrl.CloudRegion)
			if err != nil {
				log.Info(err.Error())
			}

			// Verify all credentials fields are present
			var civoCredsFailureMessage string
			switch {
			case creds.AccessKeyID == "":
				civoCredsFailureMessage = "when retrieving civo access credentials, AccessKeyID was empty - please retry your cluster creation"
			case creds.ID == "":
				civoCredsFailureMessage = "when retrieving civo access credentials, ID was empty - please retry your cluster creation"
			case creds.Name == "":
				civoCredsFailureMessage = "when retrieving civo access credentials, Name was empty - please retry your cluster creation"
			case creds.SecretAccessKeyID == "":
				civoCredsFailureMessage = "when retrieving civo access credentials, SecretAccessKeyID was empty - please retry your cluster creation"
			}
			if civoCredsFailureMessage != "" {
				// Creds failed to properly parse, so remove them
				err := civo.DeleteAccessCredentials(clctrl.KubefirstStateStoreBucketName, clctrl.CloudRegion)
				if err != nil {
					return err
				}

				// Return error
				return fmt.Errorf(civoCredsFailureMessage)
			}

			stateStoreData = types.StateStoreCredentials{
				AccessKeyID:     creds.AccessKeyID,
				SecretAccessKey: creds.SecretAccessKeyID,
				Name:            creds.Name,
				ID:              creds.ID,
			}
		case "digitalocean":
			digitaloceanConf := digitalocean.DigitaloceanConfiguration{
				Client:  digitalocean.NewDigitalocean(),
				Context: context.Background(),
			}

			creds := digitalocean.DigitaloceanSpacesCredentials{
				AccessKey:       os.Getenv("DO_SPACES_KEY"),
				SecretAccessKey: os.Getenv("DO_SPACES_SECRET"),
				Endpoint:        fmt.Sprintf("%s.digitaloceanspaces.com", "nyc3"),
			}
			err = digitaloceanConf.CreateSpaceBucket(creds, clctrl.KubefirstStateStoreBucketName)
			if err != nil {
				msg := fmt.Sprintf("error creating spaces bucket %s: %s", clctrl.KubefirstStateStoreBucketName, err)
				// telemetryShim.Transmit(useTelemetryFlag, segmentClient, segment.MetricStateStoreCreateFailed, msg)
				return fmt.Errorf(msg)
			}

			stateStoreData = types.StateStoreCredentials{
				AccessKeyID:     creds.AccessKey,
				SecretAccessKey: creds.SecretAccessKey,
				Name:            clctrl.KubefirstStateStoreBucketName,
			}

			err = clctrl.MdbCl.UpdateCluster(clctrl.ClusterName, "state_store_details", types.StateStoreDetails{
				Name:     clctrl.KubefirstStateStoreBucketName,
				Hostname: creds.Endpoint,
			})
			if err != nil {
				return err
			}

			DigitaloceanStateStoreBucketName = creds.Endpoint
		case "vultr":
			vultrConf := vultr.VultrConfiguration{
				Client:  vultr.NewVultr(),
				Context: context.Background(),
			}

			objst, err := vultrConf.CreateObjectStorage(clctrl.CloudRegion, clctrl.KubefirstStateStoreBucketName)
			if err != nil {
				// telemetryShim.Transmit(useTelemetryFlag, segmentClient, segment.MetricStateStoreCreateFailed, err.Error())
				log.Info(err.Error())
				return err
			}
			err = vultrConf.CreateObjectStorageBucket(vultr.VultrBucketCredentials{
				AccessKey:       objst.S3AccessKey,
				SecretAccessKey: objst.S3SecretKey,
				Endpoint:        objst.S3Hostname,
			}, clctrl.KubefirstStateStoreBucketName)
			if err != nil {
				return fmt.Errorf("error creating vultr state storage bucket: %s", err)
			}

			stateStoreData = types.StateStoreCredentials{
				AccessKeyID:     objst.S3AccessKey,
				SecretAccessKey: objst.S3SecretKey,
				Name:            objst.Label,
				ID:              objst.ID,
			}

			err = clctrl.MdbCl.UpdateCluster(clctrl.ClusterName, "state_store_details", types.StateStoreDetails{
				Name:     objst.Label,
				ID:       objst.ID,
				Hostname: objst.S3Hostname,
			})
			if err != nil {
				return err
			}

			VultrStateStoreBucketHostname = objst.S3Hostname
		}

		err = clctrl.MdbCl.UpdateCluster(clctrl.ClusterName, "state_store_credentials", stateStoreData)
		if err != nil {
			return err
		}

		err = clctrl.MdbCl.UpdateCluster(clctrl.ClusterName, "state_store_creds_check", true)
		if err != nil {
			return err
		}

		log.Infof("%s object storage credentials created and set", clctrl.CloudProvider)
	}

	return nil
}

// StateStoreCreate
func (clctrl *ClusterController) StateStoreCreate() error {
	cl, err := clctrl.MdbCl.GetCluster(clctrl.ClusterName)
	if err != nil {
		return err
	}

	if !cl.StateStoreCreateCheck {
		switch clctrl.CloudProvider {
		case "civo":
			// telemetryShim.Transmit(useTelemetryFlag, segmentClient, segment.MetricStateStoreCreateStarted, "")

			accessKeyId := cl.StateStoreCredentials.AccessKeyID
			log.Infof("access key id %s", accessKeyId)

			bucket, err := civo.CreateStorageBucket(accessKeyId, clctrl.KubefirstStateStoreBucketName, clctrl.CloudRegion)
			if err != nil {
				// telemetryShim.Transmit(useTelemetryFlag, segmentClient, segment.MetricStateStoreCreateFailed, err.Error())
				log.Info(err.Error())
				return err
			}

			stateStoreData := types.StateStoreDetails{
				Name: bucket.Name,
				ID:   bucket.ID,
			}
			err = clctrl.MdbCl.UpdateCluster(clctrl.ClusterName, "state_store_details", stateStoreData)
			if err != nil {
				return err
			}

			err = clctrl.MdbCl.UpdateCluster(clctrl.ClusterName, "state_store_create_check", true)
			if err != nil {
				return err
			}

			// telemetryShim.Transmit(useTelemetryFlag, segmentClient, segment.MetricStateStoreCreateCompleted, "")
			log.Infof("%s state store bucket created", clctrl.CloudProvider)
		}
	}

	return nil
}
