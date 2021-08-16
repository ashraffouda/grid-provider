package provider

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"reflect"
	"strconv"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/threefoldtech/zos/client"
	"github.com/threefoldtech/zos/pkg/crypto"
	"github.com/threefoldtech/zos/pkg/gridtypes"
	"github.com/threefoldtech/zos/pkg/gridtypes/zos"
	"github.com/threefoldtech/zos/pkg/substrate"
)

const (
	Version = 0
	// Twin      = 14
	// NodeID = 4
	// Seed      = "d161de46d136d96085906b9f3d40d08b3649c80a3e4d77f0b14d3dc6889e9dcb"
	// Substrate = "wss://explorer.devnet.grid.tf/ws"
	// rmb_url   = "tcp://127.0.0.1:6379"
)

func resourceDeployment() *schema.Resource {
	return &schema.Resource{
		// This description is used by the documentation generator and the language server.
		Description: "Sample resource in the Terraform provider scaffolding.",

		CreateContext: resourceDeploymentCreate,
		ReadContext:   resourceDeploymentRead,
		UpdateContext: resourceDeploymentUpdate,
		DeleteContext: resourceDeploymentDelete,

		Schema: map[string]*schema.Schema{
			"version": {
				Description: "Version",
				Type:        schema.TypeInt,
				Optional:    true,
				Computed:    true,
			},

			"node": {
				Description: "Node id to place deployment on",
				Type:        schema.TypeInt,
				Required:    true,
			},
			"disks": &schema.Schema{
				Type:     schema.TypeList,
				Optional: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"name": &schema.Schema{
							Type:     schema.TypeString,
							Required: true,
						},
						"size": &schema.Schema{
							Type:     schema.TypeInt,
							Required: true,
						},
						"description": &schema.Schema{
							Type:     schema.TypeString,
							Required: true,
						},
						"version": {
							Description: "Version",
							Type:        schema.TypeInt,
							Optional:    true,
							Computed:    true,
						},
					},
				},
			},
			"zdbs": &schema.Schema{
				Type:     schema.TypeList,
				Optional: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"name": &schema.Schema{
							Type:     schema.TypeString,
							Required: true,
						},
						"password": &schema.Schema{
							Type:     schema.TypeString,
							Required: true,
						},
						"size": &schema.Schema{
							Type:     schema.TypeInt,
							Required: true,
						},
						"description": &schema.Schema{
							Type:     schema.TypeString,
							Required: true,
						},
						"version": {
							Description: "Version",
							Type:        schema.TypeInt,
							Optional:    true,
							Computed:    true,
						},
						"mode": {
							Description: "Mode of the zdb",
							Type:        schema.TypeString,
							Optional:    true,
							Computed:    true,
						},
					},
				},
			},
			"vms": &schema.Schema{
				Type:     schema.TypeList,
				Required: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"name": &schema.Schema{
							Type:     schema.TypeString,
							Required: true,
						},
						"flist": &schema.Schema{
							Type:     schema.TypeString,
							Required: true,
						},
						"version": {
							Description: "Version",
							Type:        schema.TypeInt,
							Optional:    true,
							Computed:    true,
						},
						"ip": {
							Description: "IP",
							Type:        schema.TypeString,
							Optional:    true,
							Computed:    true,
						},
						"cpu": {
							Description: "CPU size",
							Type:        schema.TypeInt,
							Optional:    true,
						},
						"memory": {
							Description: "Memory size",
							Type:        schema.TypeInt,
							Optional:    true,
						},
						"entrypoint": {
							Description: "VM entry point",
							Type:        schema.TypeString,
							Optional:    true,
						},
						"mounts": &schema.Schema{
							Type:     schema.TypeList,
							Optional: true,
							Elem: &schema.Resource{
								Schema: map[string]*schema.Schema{
									"disk_name": &schema.Schema{
										Type:     schema.TypeString,
										Required: true,
									},
									"mount_point": &schema.Schema{
										Type:     schema.TypeString,
										Required: true,
									},
								},
							},
						},
						"env_vars": &schema.Schema{
							Type:     schema.TypeList,
							Optional: true,
							Elem: &schema.Resource{
								Schema: map[string]*schema.Schema{
									"key": &schema.Schema{
										Type:     schema.TypeString,
										Required: true,
									},
									"value": &schema.Schema{
										Type:     schema.TypeString,
										Required: true,
									},
								},
							},
						},
					},
				},
			},
			"used_ips": {
				Type:     schema.TypeList,
				Computed: true,
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
			},
			"ip_range": {
				Type:     schema.TypeString,
				Required: true,
			},
			"network_name": {
				Type:     schema.TypeString,
				Required: true,
			},
		},
	}
}

func getFreeIP(ipRange gridtypes.IPNet, usedIPs []string) (string, error) {
	i := 2
	l := len(ipRange.IP)
	for i < 255 {
		ip := ipNet(ipRange.IP[l-4], ipRange.IP[l-3], ipRange.IP[l-2], byte(i), 32)
		ipStr := fmt.Sprintf("%d.%d.%d.%d", ip.IP[l-4], ip.IP[l-3], ip.IP[l-2], ip.IP[l-1])
		log.Printf("ip string: %s\n", ipStr)
		if !isInStr(usedIPs, ipStr) {
			return ipStr, nil
		}
		i += 1
	}
	return "", errors.New("all ips are used")
}

func waitDeployment(ctx context.Context, nodeClient *client.NodeClient, deploymentID uint64) error {
	done := false
	for start := time.Now(); time.Since(start) < 4*time.Minute; {
		done = true
		dl, err := nodeClient.DeploymentGet(ctx, deploymentID)
		if err != nil {
			return err
		}
		for idx, wl := range dl.Workloads {
			if wl.Result.State == "" {
				done = false
				continue
			}
			if wl.Result.State != gridtypes.StateOk {
				return errors.New(fmt.Sprintf("workload %d failed within deployment %d with error %s", idx, deploymentID, wl.Result.Error))
			}
		}
		if done {
			return nil
		}
	}
	return errors.New(fmt.Sprintf("waiting for deployment %d timedout", deploymentID))
}

func resourceDeploymentCreate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	ipRangeStr := d.Get("ip_range").(string)
	ipRange, err := gridtypes.ParseIPNet(ipRangeStr)
	usedIPsIfs := d.Get("used_ips").([]interface{})
	usedIPs := make([]string, 0)
	for _, ip := range usedIPsIfs {
		usedIPs = append(usedIPs, ip.(string))
	}
	networkName := d.Get("network_name").(string)

	if err != nil {
		return diag.FromErr(err)
	}
	apiClient := meta.(*apiClient)
	identity, err := substrate.IdentityFromPhrase(string(apiClient.mnemonics))
	if err != nil {
		return diag.FromErr(err)
	}
	userSK, err := identity.SecureKey()
	if err != nil {
		return diag.FromErr(err)
	}

	cl := apiClient.client

	var diags diag.Diagnostics
	nodeID := uint32(d.Get("node").(int))

	disks := d.Get("disks").([]interface{})
	vms := d.Get("vms").([]interface{})

	workloads := []gridtypes.Workload{}
	updated_disks := make([]interface{}, 0)
	for _, disk := range disks {
		data := disk.(map[string]interface{})
		data["version"] = Version
		workload := gridtypes.Workload{
			Name:        gridtypes.Name(data["name"].(string)),
			Version:     Version,
			Type:        zos.ZMountType,
			Description: data["description"].(string),
			Data: gridtypes.MustMarshal(zos.ZMount{
				Size: gridtypes.Unit(data["size"].(int)) * gridtypes.Gigabyte,
			}),
		}
		updated_disks = append(updated_disks, data)
		workloads = append(workloads, workload)
	}
	d.Set("disks", updated_disks)

	zdbs := d.Get("zdbs").([]interface{})
	updated_zdbs := make([]interface{}, 0)
	for _, zdb := range zdbs {
		data := zdb.(map[string]interface{})
		pwd, err := crypto.EncryptECDH([]byte(data["password"].(string)), userSK, identity.PublicKey)
		if err != nil {
			return diag.FromErr(err)
		}
		data["version"] = Version
		workload := gridtypes.Workload{
			Name:        gridtypes.Name(data["name"].(string)),
			Type:        zos.ZDBType,
			Description: data["description"].(string),
			Version:     Version,
			Data: gridtypes.MustMarshal(zos.ZDB{
				Size:     gridtypes.Unit(data["size"].(int)),
				Mode:     zos.ZDBMode(data["mode"].(string)),
				Password: hex.EncodeToString(pwd),
			}),
		}
		updated_zdbs = append(updated_zdbs, data)
		workloads = append(workloads, workload)
	}
	d.Set("zdb", updated_zdbs)

	updated_vms := make([]interface{}, 0)
	for _, vm := range vms {
		data := vm.(map[string]interface{})
		data["version"] = Version
		mount_points := data["mounts"].([]interface{})
		mounts := []zos.MachineMount{}
		for _, mount_point := range mount_points {
			point := mount_point.(map[string]interface{})
			mount := zos.MachineMount{Name: gridtypes.Name(point["disk_name"].(string)), Mountpoint: point["mount_point"].(string)}
			mounts = append(mounts, mount)
		}

		env_vars := data["env_vars"].([]interface{})
		envVars := make(map[string]string)

		for _, env_var := range env_vars {
			envVar := env_var.(map[string]interface{})
			envVars[envVar["key"].(string)] = envVar["value"].(string)
		}
		freeIP, err := getFreeIP(ipRange, usedIPs)
		if err != nil {
			return diag.FromErr(err)
		}
		usedIPs = append(usedIPs, freeIP)
		data["ip"] = freeIP
		workload := gridtypes.Workload{
			Version: Version,
			Name:    gridtypes.Name(data["name"].(string)),
			Type:    zos.ZMachineType,
			Data: gridtypes.MustMarshal(zos.ZMachine{
				FList: data["flist"].(string),
				Network: zos.MachineNetwork{
					Interfaces: []zos.MachineInterface{
						{
							Network: gridtypes.Name(networkName),
							IP:      net.ParseIP(freeIP),
						},
					},
					Planetary: true,
				},
				ComputeCapacity: zos.MachineCapacity{
					CPU:    uint8(data["cpu"].(int)),
					Memory: gridtypes.Unit(uint(data["memory"].(int))) * gridtypes.Megabyte,
				},
				Entrypoint: data["entrypoint"].(string),
				Mounts:     mounts,
				Env:        envVars,
			}),
		}
		updated_vms = append(updated_vms, data)
		workloads = append(workloads, workload)

	}

	d.Set("vms", updated_vms)

	dl := gridtypes.Deployment{
		Version: Version,
		TwinID:  uint32(apiClient.twin_id), //LocalTwin,
		// this contract id must match the one on substrate
		Workloads: workloads,
		SignatureRequirement: gridtypes.SignatureRequirement{
			WeightRequired: 1,
			Requests: []gridtypes.SignatureRequest{
				{
					TwinID: apiClient.twin_id,
					Weight: 1,
				},
			},
		},
	}

	if err := dl.Valid(); err != nil {
		return diag.FromErr(errors.New("invalid: " + err.Error()))
	}
	//return
	if err := dl.Sign(apiClient.twin_id, userSK); err != nil {
		return diag.FromErr(err)
	}

	hash, err := dl.ChallengeHash()
	log.Printf("[DEBUG] HASH: %#v", hash)

	if err != nil {
		return diag.FromErr(errors.New("failed to create hash"))
	}

	hashHex := hex.EncodeToString(hash)
	fmt.Printf("hash: %s\n", hashHex)
	// create contract
	sub, err := substrate.NewSubstrate(apiClient.substrate_url)
	if err != nil {
		return diag.FromErr(err)
	}
	nodeInfo, err := sub.GetNode(nodeID)
	if err != nil {
		return diag.FromErr(err)
	}

	node := client.NewNodeClient(uint32(nodeInfo.TwinID), cl)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	log.Printf("[DEBUG] NodeId: %#v", nodeID)
	log.Printf("[DEBUG] HASH: %#v", hashHex)
	contractID, err := sub.CreateContract(&identity, nodeID, nil, hashHex, 0)
	if err != nil {
		return diag.FromErr(err)
	}
	dl.ContractID = contractID // from substrate

	err = node.DeploymentDeploy(ctx, dl)
	if err != nil {
		return diag.FromErr(err)
	}
	err = waitDeployment(ctx, node, dl.ContractID)
	if err != nil {
		return diag.FromErr(err)
	}
	got, err := node.DeploymentGet(ctx, dl.ContractID)
	if err != nil {
		return diag.FromErr(err)
	}
	enc := json.NewEncoder(log.Writer())
	enc.SetIndent("", "  ")
	enc.Encode(got)
	d.Set("used_ips", usedIPs)
	d.SetId(strconv.FormatUint(contractID, 10))
	// resourceDiskRead(ctx, d, meta)

	return diags
}

func flattenDiskData(workload gridtypes.Workload) (map[string]interface{}, error) {
	if workload.Type == zos.ZMountType {
		wl := make(map[string]interface{})
		data, err := workload.WorkloadData()
		if err != nil {
			return nil, err
		}
		wl["name"] = workload.Name
		wl["size"] = data.(*zos.ZMount).Size / gridtypes.Gigabyte
		wl["description"] = workload.Description
		wl["version"] = workload.Version
		return wl, nil
	}

	return nil, errors.New("The wrokload is not of type zos.ZMountType")
}
func flattenVMData(workload gridtypes.Workload) (map[string]interface{}, error) {
	if workload.Type == zos.ZMachineType {
		wl := make(map[string]interface{})
		workloadData, err := workload.WorkloadData()
		if err != nil {
			return nil, err
		}
		data := workloadData.(*zos.ZMachine)

		mounts := make([]map[string]interface{}, 0)
		for diskName, mountPoint := range data.Mounts {
			mount := map[string]interface{}{
				"disk_name": diskName, "mount_point": mountPoint,
			}
			mounts = append(mounts, mount)
		}
		wl["cpu"] = data.ComputeCapacity.CPU
		wl["memory"] = data.ComputeCapacity.Memory
		wl["mounts"] = mounts
		wl["name"] = workload.Name
		wl["flist"] = data.FList
		wl["version"] = workload.Version
		wl["description"] = workload.Description
		return wl, nil
	}

	return nil, errors.New("The wrokload is not of type zos.ZMachineType")
}

func resourceDeploymentRead(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	// use the meta value to retrieve your client from the provider configure method
	apiClient := meta.(*apiClient)
	cl := apiClient.client
	var diags diag.Diagnostics
	sub, err := substrate.NewSubstrate(apiClient.substrate_url)
	if err != nil {
		return diag.FromErr(err)
	}
	nodeID := uint32(d.Get("node").(int))
	nodeInfo, err := sub.GetNode(nodeID)
	if err != nil {
		return diag.FromErr(err)
	}

	node := client.NewNodeClient(uint32(nodeInfo.TwinID), cl)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	contractId, err := strconv.ParseUint(d.Id(), 10, 64)
	deployment, err := node.DeploymentGet(ctx, contractId)
	if err != nil {
		return diag.FromErr(err)
	}

	disks := make([]map[string]interface{}, 0, 0)
	vms := make([]map[string]interface{}, 0, 0)
	for _, workload := range deployment.Workloads {
		if workload.Type == zos.ZMountType {
			flattened, err := flattenDiskData(workload)
			if err != nil {
				return diag.FromErr(err)
			}
			disks = append(disks, flattened)

		}
		if workload.Type == zos.ZMachineType {
			flattened, err := flattenVMData(workload)
			if err != nil {
				return diag.FromErr(err)
			}
			vms = append(vms, flattened)
		}
	}
	d.Set("vms", vms)
	d.Set("disks", disks)
	d.Set("version", deployment.Version)
	return diags
}

func diskHasChanged(disk map[string]interface{}, oldDisks []interface{}) (bool, map[string]interface{}) {
	for _, d := range oldDisks {
		diskData := d.(map[string]interface{})
		if diskData["name"] == disk["name"] {
			if diskData["description"] != disk["description"] || diskData["size"] != disk["size"] {
				return true, diskData
			} else {
				return false, diskData
			}

		}

	}
	return false, nil
}
func zdbHasChanged(zdb map[string]interface{}, oldZdbs []interface{}) (bool, map[string]interface{}) {
	for _, d := range oldZdbs {
		zdbData := d.(map[string]interface{})
		if zdbData["name"] == zdb["name"] {
			if zdbData["password"] != zdb["password"] || zdbData["size"] != zdb["size"] || zdbData["description"] != zdb["description"] || zdbData["mode"] != zdb["mode"] {
				return true, zdbData
			} else {
				return false, zdbData
			}

		}

	}
	return false, nil
}

func vmHasChanged(vm map[string]interface{}, oldVms []interface{}) (bool, map[string]interface{}) {
	for _, machine := range oldVms {
		vmData := machine.(map[string]interface{})
		if vmData["name"] == vm["name"] && vmData["flist"] == vm["flist"] {
			// if vmData.HasChange("cpu") || vmData.HasChange("memory") || vmData.HasChange("entrypoint") || vmData.HasChange("mounts") || vmData.HasChange("env_vars") {
			if vmData["cpu"] != vm["cpu"] || vmData["memory"] != vm["memory"] || vmData["entrypoint"] != vm["entrypoint"] || reflect.DeepEqual(vmData["mounts"], vm["mounts"]) || reflect.DeepEqual(vmData["env_vars"], vm["env_vars"]) {
				return true, vmData
			} else {
				return false, vmData
			}

		}

	}
	return false, nil

}
func resourceDeploymentUpdate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	apiClient := meta.(*apiClient)
	identity, err := substrate.IdentityFromPhrase(string(apiClient.mnemonics))
	if err != nil {
		return diag.FromErr(err)
	}
	userSK, err := identity.SecureKey()
	if err != nil {
		return diag.FromErr(err)
	}
	cl := apiClient.client

	var diags diag.Diagnostics
	// twinID := d.Get("twinid").(string)
	if d.HasChange("node") {
		return diag.FromErr(errors.New("changing node is not supported, you need to destroy the deployment and reapply it again"))
	}
	oldDisks, _ := d.GetChange("disks")
	oldVms, _ := d.GetChange("vms")
	nodeID := uint32(d.Get("node").(int))

	disks := d.Get("disks").([]interface{})
	vms := d.Get("vms").([]interface{})
	updatedDisks := make([]interface{}, 0)

	workloads := []gridtypes.Workload{}
	// workloads = append(workloads, network())
	for _, disk := range disks {
		data := disk.(map[string]interface{})
		version := 0

		changed, oldDisk := diskHasChanged(data, oldDisks.([]interface{}))
		if changed && oldDisk != nil {
			version = oldDisk["version"].(int) + 1
		} else if !changed && oldDisk != nil {
			version = oldDisk["version"].(int)
		}
		data["version"] = version
		workload := gridtypes.Workload{
			Name:        gridtypes.Name(data["name"].(string)),
			Version:     version,
			Type:        zos.ZMountType,
			Description: data["description"].(string),
			Data: gridtypes.MustMarshal(zos.ZMount{
				Size: gridtypes.Unit(data["size"].(int)) * gridtypes.Gigabyte,
			}),
		}
		workloads = append(workloads, workload)
		updatedDisks = append(updatedDisks, data)
	}
	d.Set("disks", updatedDisks)

	oldZdbs, _ := d.GetChange("zdbs")
	zdbs := d.Get("zdbs").([]interface{})
	updatedZdbs := make([]interface{}, 0)
	for _, zdb := range zdbs {
		data := zdb.(map[string]interface{})
		version := 0

		changed, oldZdb := zdbHasChanged(data, oldZdbs.([]interface{}))
		if changed && oldZdb != nil {
			version = oldZdb["version"].(int) + 1
		} else if !changed && oldZdb != nil {
			version = oldZdb["version"].(int)
		}
		data["version"] = version
		pwd, err := crypto.EncryptECDH([]byte(data["password"].(string)), userSK, identity.PublicKey)
		if err != nil {
			return diag.FromErr(err)
		}
		workload := gridtypes.Workload{
			Type:        zos.ZDBType,
			Name:        gridtypes.Name(data["name"].(string)),
			Description: data["description"].(string),
			Version:     Version,
			Data: gridtypes.MustMarshal(zos.ZDB{
				Size:     gridtypes.Unit(data["size"].(int)),
				Mode:     zos.ZDBMode(data["mode"].(string)),
				Password: hex.EncodeToString(pwd),
			}),
		}
		workloads = append(workloads, workload)
		updatedZdbs = append(updatedZdbs, data)
	}
	d.Set("zdbs", updatedZdbs)

	updatedVms := make([]interface{}, 0)
	for _, vm := range vms {
		data := vm.(map[string]interface{})
		version := 0

		changed, oldVmachine := vmHasChanged(data, oldVms.([]interface{}))
		if changed && oldVmachine != nil {
			version = oldVmachine["version"].(int) + 1
		} else if !changed && oldVmachine != nil {
			version = oldVmachine["version"].(int)
		}
		data["version"] = version
		mount_points := data["mounts"].([]interface{})
		mounts := []zos.MachineMount{}
		for _, mount_point := range mount_points {
			point := mount_point.(map[string]interface{})
			mount := zos.MachineMount{Name: gridtypes.Name(point["disk_name"].(string)), Mountpoint: point["mount_point"].(string)}
			mounts = append(mounts, mount)
		}

		env_vars := data["env_vars"].([]interface{})
		envVars := make(map[string]string)

		for _, env_var := range env_vars {
			envVar := env_var.(map[string]interface{})
			envVars[envVar["key"].(string)] = envVar["value"].(string)
		}
		workload := gridtypes.Workload{
			Version: version,
			Name:    gridtypes.Name(data["name"].(string)),
			Type:    zos.ZMachineType,
			Data: gridtypes.MustMarshal(zos.ZMachine{
				FList: data["flist"].(string),
				Network: zos.MachineNetwork{
					Interfaces: []zos.MachineInterface{
						{
							Network: "network",
							IP:      net.ParseIP("10.1.1.3"),
						},
					},
					Planetary: true,
				},
				ComputeCapacity: zos.MachineCapacity{
					CPU:    uint8(data["cpu"].(int)),
					Memory: gridtypes.Unit(uint(data["memory"].(int))) * gridtypes.Megabyte,
				},
				Entrypoint: data["entrypoint"].(string),
				Mounts:     mounts,
				Env:        envVars,
			}),
		}
		workloads = append(workloads, workload)
		updatedVms = append(updatedVms, data)
	}
	d.Set("vms", updatedVms)
	dlVersion := d.Get("version").(int)

	dl := gridtypes.Deployment{
		Version: dlVersion + 1,
		TwinID:  uint32(apiClient.twin_id), //LocalTwin,
		// this contract id must match the one on substrate
		Workloads: workloads,
		SignatureRequirement: gridtypes.SignatureRequirement{
			WeightRequired: 1,
			Requests: []gridtypes.SignatureRequest{
				{
					TwinID: apiClient.twin_id,
					Weight: 1,
				},
			},
		},
	}

	if err := dl.Valid(); err != nil {
		return diag.FromErr(errors.New("invalid: " + err.Error()))
	}
	//return
	if err := dl.Sign(apiClient.twin_id, userSK); err != nil {
		return diag.FromErr(err)
	}

	hash, err := dl.ChallengeHash()
	log.Printf("[DEBUG] HASH: %#v", hash)

	if err != nil {
		return diag.FromErr(errors.New("failed to create hash"))
	}

	hashHex := hex.EncodeToString(hash)
	fmt.Printf("hash: %s\n", hashHex)
	// create contract
	sub, err := substrate.NewSubstrate(apiClient.substrate_url)
	if err != nil {
		return diag.FromErr(err)
	}
	nodeInfo, err := sub.GetNode(nodeID)
	if err != nil {
		return diag.FromErr(err)
	}

	node := client.NewNodeClient(uint32(nodeInfo.TwinID), cl)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	total, used, err := node.Counters(ctx)
	if err != nil {
		return diag.FromErr(err)
	}

	fmt.Printf("Total: %+v\nUsed: %+v\n", total, used)
	contractID, err := strconv.ParseUint(d.Id(), 10, 64)
	if err != nil {
		return diag.FromErr(err)
	}
	contractID, err = sub.UpdateContract(&identity, contractID, nil, hashHex)
	if err != nil {
		return diag.FromErr(err)
	}
	dl.ContractID = contractID // from substrate

	err = node.DeploymentUpdate(ctx, dl)
	if err != nil {
		return diag.FromErr(err)
	}

	err = waitDeployment(ctx, node, dl.ContractID)
	if err != nil {
		return diag.FromErr(err)
	}

	got, err := node.DeploymentGet(ctx, dl.ContractID)
	if err != nil {
		return diag.FromErr(err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(got)
	d.SetId(strconv.FormatUint(contractID, 10))
	// resourceDiskRead(ctx, d, meta)

	return diags
}

func resourceDeploymentDelete(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	apiClient := meta.(*apiClient)
	nodeID := uint32(d.Get("node").(int))
	identity, err := substrate.IdentityFromPhrase(string(apiClient.mnemonics))
	if err != nil {
		return diag.FromErr(err)
	}

	if err != nil {
		return diag.FromErr(err)
	}
	cl := apiClient.client
	sub, err := substrate.NewSubstrate(apiClient.substrate_url)
	if err != nil {
		return diag.FromErr(err)
	}
	nodeInfo, err := sub.GetNode(nodeID)
	if err != nil {
		return diag.FromErr(err)
	}

	node := client.NewNodeClient(uint32(nodeInfo.TwinID), cl)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	contractID, err := strconv.ParseUint(d.Id(), 10, 64)
	if err != nil {
		return diag.FromErr(err)
	}
	err = sub.CancelContract(&identity, contractID)
	if err != nil {
		return diag.FromErr(err)
	}

	err = node.DeploymentDelete(ctx, contractID)
	if err != nil {
		return diag.FromErr(err)
	}
	d.SetId("")

	return diags

}
