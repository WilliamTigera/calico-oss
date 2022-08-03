// Copyright 2019, 2021-2022 Tigera Inc. All rights reserved.

package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/projectcalico/calico/libcalico-go/lib/health"
	"github.com/projectcalico/calico/libcalico-go/lib/logutils"

	calicoclient "github.com/tigera/api/pkg/client/clientset_generated/clientset"
	clientv3 "github.com/tigera/api/pkg/client/clientset_generated/clientset/typed/projectcalico/v3"
	"github.com/projectcalico/calico/firewall-integration/pkg/config"
	"github.com/projectcalico/calico/firewall-integration/pkg/controllers/fortimanager"
	"github.com/projectcalico/calico/firewall-integration/pkg/controllers/panorama"
	panutils "github.com/projectcalico/calico/firewall-integration/pkg/controllers/panorama/utils"
	fortilib "github.com/projectcalico/calico/firewall-integration/pkg/fortimanager"
)

const jsonContentType = "application/json"

const (
	NSTigeraFirewallController = "tigera-firewall-controller"
)

// These are filled out during the build process (using git describe output)
var VERSION, BUILD_DATE, GIT_DESCRIPTION, GIT_REVISION string
var version bool

func PrintVersion() error {
	fmt.Println("Version:     ", VERSION)
	fmt.Println("Build date:  ", BUILD_DATE)
	fmt.Println("Git tag ref: ", GIT_DESCRIPTION)
	fmt.Println("Git commit:  ", GIT_REVISION)
	return nil
}

func init() {
	// Add a flag to check the version.
	flag.BoolVar(&version, "version", false, "Display version")
}

func main() {

	flag.Parse()
	if version {
		_ = PrintVersion()
		os.Exit(0)
	}

	logLevel := log.InfoLevel
	logLevelStr := os.Getenv("LOG_LEVEL")
	log.SetFormatter(&logutils.Formatter{})
	parsedLogLevel, err := log.ParseLevel(logLevelStr)
	if err == nil {
		logLevel = parsedLogLevel
	} else {
		log.Warnf("Could not parse log level %v, setting log level to %v", logLevelStr, logLevel)
	}
	log.SetLevel(logLevel)
	// Install a hook that adds file/line no information.
	log.AddHook(&logutils.ContextHook{})

	// Signal setup.
	sigs := make(chan os.Signal, 1)
	// Handle INT/TERM for now.
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		sig := <-sigs
		log.Debugf("Signal received: %v", sig)
		cancel()
	}()

	// Health setup.
	h := health.NewHealthAggregator()
	h.ServeHTTP(true, "0.0.0.0", 9099)

	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Error reading configuration: %s", err)
		return
	}
	k8sClient, err := getKubernetesClient(cfg.KubeConfig)
	if err != nil {
		log.Fatalf("Error creating kubernetes client: %s", err)
		return
	}

	calicoClient, err := getCalicoClient(cfg.KubeConfig)
	if err != nil {
		log.Fatalf("Error creating calico api server client: %s", err)
		return
	}

	var wg sync.WaitGroup
	enabledControllers := strings.Split(cfg.EnabledControllers, ",")

	log.Debug("Starting controllers")
	for _, controllerType := range enabledControllers {
		switch controllerType {
		case "panorama-policy":
			wg.Add(1)
			log.Debug("Starting Panorama policy controller")
			panCli, err := panutils.NewPANWClient(cfg)
			if err != nil {
				log.Fatalf("Panorama client not accessible, with error: %s", err.Error())
				return
			}
			fpic, err := panorama.NewFirewallPolicyIntegrationController(ctx, k8sClient, calicoClient, panCli, cfg, h, &wg)
			if err != nil {
				log.Fatalf("Failed to configure firewall policy integration controller, with error: %s", err.Error())
				return
			}
			// Run the firewall integration controller.
			go fpic.Run()

		case "panorama-address-groups":
			wg.Add(1)
			log.Debug("Starting Panorama address groups controller")
			panCli, err := panutils.NewPANWClient(cfg)
			if err != nil {
				log.WithError(err).Fatal("Failed to define Panorama address groups controller")
			}
			dagc, err := panorama.NewDynamicAddressGroupsController(ctx, k8sClient, calicoClient, panCli, cfg, h, &wg)
			if err != nil {
				log.Fatal("Failed to configure Panorama address groups controller")
				return
			}
			// Run the address groups controller.
			go dagc.Run()

		case "panorama":
			wg.Add(1)
			log.Debug("Starting Panorama integration controller")
			pc := panorama.NewPanoramaController(ctx, cfg, h)
			pc.Run()
			wg.Done()

		case "fortinet":
			wg.Add(1)
			log.Debugf("Attempting to read FortiGate config at %v", cfg.FwFortiGateConfig)
			fgts, err := getFortiDevicesConfig(cfg.FwFortiGateConfig, k8sClient, false)
			if err != nil {
				log.WithError(err).Error("Failed to get FortiGate device configs")
			}

			log.Debugf("Attempting to read FortiManager config at %v", cfg.FwFortiMgrConfig)
			fmgrs, err := getFortiDevicesConfig(cfg.FwFortiMgrConfig, k8sClient, true)
			if err != nil {
				log.WithError(err).Error("Failed to get FortiMgr device configs")
			}

			log.Debugf("Attempting to read FortiManager policy-package controller config at %v", cfg.FwFortiMgrEWConfig)
			fmgrEW, err := getFortiDevicesConfig(cfg.FwFortiMgrEWConfig, k8sClient, true)
			if err != nil {
				log.WithError(err).Error("Failed to get FortiMgr device configs")
			}

			if fgts == nil && fmgrs == nil && fmgrEW == nil {
				log.Fatal("Failed to get FortiGate and FortiManager device configs. No device configured.")
				return
			}
			log.Debug("Starting Fortinet device integration controller")
			fortiGClients := make(map[string]fortilib.FortiFWClientApi)
			fortiMClients := make(map[string]fortilib.FortiFWClientApi)
			for _, fgt := range fgts {
				frclient := fortilib.NewFortiGateRestClient(jsonContentType, cfg.FwInsecureSkipVerify).(*fortilib.FortiGateRestClient)
				fclient := fortilib.NewFortiGateClient(fgt.Ip, fgt.Ip, fgt.ApiKey, frclient)
				fortiGClients[fgt.Ip] = fclient
			}

			for _, fmgr := range fmgrs {
				// FortiManager configs are common for East-West controller and Fortinet Firewall[selector] controller
				// we expect user to provide different config map for East-West and Fortinet Firewall controller
				// for same FortiManager device.
				// Hence, skip processing East-West controllers device configMap for selector controller
				if fmgr.PkgName != "" && fmgr.Tier != "" {
					continue
				}

				log.Debug("Starting FortiManager N/S controller")
				fclient, err := fortilib.NewFortiManagerClient(fmgr.Ip, fmgr.Ip, fmgr.Username, fmgr.Password, fmgr.Adom, true)
				if err != nil {
					log.WithError(err).Fatal("Error when creating FortiManager client")
				}
				fortiMClients[fmgr.Ip] = fclient
				fmclient := fclient.(*fortilib.FortiManagerClient)
				defer fmclient.Logout()
			}

			// Merge FortiManager and FortiGate clients into single map
			for dev, client := range fortiMClients {
				fortiGClients[dev] = client
			}

			if len(fortiGClients) >= 1 {
				log.Debug("Starting FortiGate N/S controller")
				fcSelector := fortimanager.NewSelectorsController(ctx, cfg, h, k8sClient, fortiGClients, calicoClient)
				fcSelector.Run()
			}
			// Only one FortiManager instance shall be used for access control of East-west traffic on K8s Cluster.
			// If more than one Instance of FortiManager is provided through configMap, Fail to start controller.
			if len(fmgrEW) == 1 {
				fmgr := fmgrEW[0]
				fclient, err := fortilib.NewFortiManagerClient(fmgr.Ip, fmgr.Ip, fmgr.Username, fmgr.Password, fmgr.Adom, true)
				if err != nil {
					log.WithError(err).Fatal("Error when creating FortiManager client")
				}
				fmclient := fclient.(*fortilib.FortiManagerClient)
				defer fmclient.Logout()

				fcEastWest := fortimanager.NewEastWestController(ctx, cfg, h, fclient, calicoClient, fmgr.Tier, fmgr.PkgName)
				fcEastWest.Run()
			} else {
				log.Warn("Not starting FortiManager EW controller. Only one FortiManager instance is supported.")
			}

			wg.Done()
		default:
			log.Infof("Failed to deploy controller. %s may not exist", controllerType)
		}
	}
	log.Info("Waiting for goroutines to finish...")
	wg.Wait()
}

func getKubernetesClient(kubeconfig string) (*kubernetes.Clientset, error) {
	// Now build the Kubernetes client, we support in-cluster config and kubeconfig
	// as means of configuring the client.
	k8sconfig, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build kubernetes client config: %s", err)
	}

	// Get Kubernetes clientset
	k8sClientset, err := kubernetes.NewForConfig(k8sconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build kubernetes client: %s", err)
	}
	return k8sClientset, nil
}

func getCalicoClient(kubeconfig string) (clientv3.ProjectcalicoV3Interface, error) {

	//Build the calico enterprise client
	calicoConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build calico api client config: %s", err)
	}
	// Get Calico Api server Client
	calicoClient, err := calicoclient.NewForConfig(calicoConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build calico api client: %s", err)
	}

	return calicoClient.ProjectcalicoV3(), nil
}

//  getFortiDevicesConfig retrieves the devies config from config and Secrets
// Configmap Fortmat for FortiGate devices
/*
  tigera.firewall.fortinet: |
    - name: prod-east1
      ip: 1.2.3.1
      apikey:
        secretKeyRef:
          name: fortigate-east1
          key: apikey-fortigate-east1
    - name: prod-east2
      ip: 1.2.3.2
      apikey:
        secretKeyRef:
          name: fortigate-east2
          key: apikey-fortigate-east2
  tigera.firewall.fortimgr: |
    - name: prod-east1
      ip: 1.2.4.1
      username: api_user
      adom: root
      password:
        secretKeyRef:
          name: fortimgr-east1
          key: pwd-fortimgr-east1

  In namespace tigera-firewall-controller, create secret name as `fortimgr-east1` for storing password
  access FortiMgr device prod-east1 [1.2.4.1]
*/

func getFortiDevicesConfig(fortiCfgPath string, k8sClient *kubernetes.Clientset, isFortiMgr bool) ([]fortilib.FwFortiDevConfig, error) {
	// Read ConfigMap from the File
	data, err := ioutil.ReadFile(fortiCfgPath)
	if err != nil {
		log.WithError(err).Errorf("Failed to read config file %v", fortiCfgPath)
		return nil, err
	}

	fwFortiDevCfgs := make([]fortilib.FwFortiDevConfig, 0)
	// For FortiGate devices, Read "ApiKey" from secrets, in namespace "tigera-firewall-controller"
	if !isFortiMgr {
		fgtsCfg := []fortilib.FortiGateConfig{}
		err = yaml.Unmarshal(data, &fgtsCfg)
		if err != nil {
			log.WithError(err).Errorf("Error unmarshalling FortiGate config file :%v", fortiCfgPath)
			return nil, err
		}
		for _, fg := range fgtsCfg {
			// Get Secret key Name from configMap
			key := fg.ApiKey.FortiSecRefKey.Key
			name := fg.ApiKey.FortiSecRefKey.Name
			// Get Apikey value from Secret
			data, err := k8sClient.CoreV1().Secrets(NSTigeraFirewallController).Get(context.Background(), name, metav1.GetOptions{})
			if err != nil {
				log.WithError(err).Errorf("Failed to retrieve secrets for :%v", name)
				continue
			}
			fgtCfg := fortilib.FwFortiDevConfig{
				Name:   fg.Name,
				Ip:     fg.Ip,
				ApiKey: string(data.Data[key]),
			}
			fwFortiDevCfgs = append(fwFortiDevCfgs, fgtCfg)
		}
		return fwFortiDevCfgs, nil
	}
	// Retrieve FortiManager configs
	// For FortiMgr devices, Read "Password" from secrets, in namespace "tigera-firewall-controller"
	fmgrsCfg := []fortilib.FortiMgrConfig{}
	err = yaml.Unmarshal(data, &fmgrsCfg)
	if err != nil {
		log.WithError(err).Errorf("Error unmarshalling FortiMgr config file :%v", fortiCfgPath)
		return nil, err
	}
	for _, fm := range fmgrsCfg {
		key := fm.Password.FortiSecRefKey.Key
		name := fm.Password.FortiSecRefKey.Name
		data, err := k8sClient.CoreV1().Secrets(NSTigeraFirewallController).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			log.WithError(err).Errorf("Failed to retrieve secrets for :%v", name)
			continue
		}
		fmgrCfg := fortilib.FwFortiDevConfig{
			Name:     fm.Name,
			Ip:       fm.Ip,
			Adom:     fm.Adom,
			Username: fm.Username,
			Password: string(data.Data[key]),
			PkgName:  fm.PackageName,
			Tier:     fm.Tier,
		}
		fwFortiDevCfgs = append(fwFortiDevCfgs, fmgrCfg)
	}

	return fwFortiDevCfgs, nil
}
