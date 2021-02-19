package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/loadbalancer/v2/listeners"
	"github.com/gophercloud/gophercloud/openstack/loadbalancer/v2/loadbalancers"
	"github.com/gophercloud/gophercloud/openstack/loadbalancer/v2/monitors"
	"github.com/gophercloud/gophercloud/openstack/loadbalancer/v2/pools"
	"github.com/gophercloud/gophercloud/pagination"
	"k8s.io/apimachinery/pkg/util/wait"
)

func main() {
	var loadBalancerID, listenerID, targetProtocol string
	var switchDefaultPool, deleteOldPool bool
	flag.StringVar(&loadBalancerID, "loadBalancerID", "", "Modify the pools of all listeners of t he given loadbalancer")
	flag.StringVar(&listenerID, "listenerID", "", "Modify the pools of the given listener")
	flag.StringVar(&targetProtocol, "protocol", "PROXY", "The protocol to which the pools should be changed to")
	flag.BoolVar(&switchDefaultPool, "switch-default-pool", false, "Change the default pool of the listner to the new pool")
	flag.BoolVar(&deleteOldPool, "delete", false, "Delete the old pool after switching (implies -switch-default-pool)")
	flag.Parse()

	if listenerID == "" && loadBalancerID == "" {
		log.Fatal("Specify at least -loadBalancerID or -listenerID")
	}

	opts, err := openstack.AuthOptionsFromEnv()
	if err != nil {
		log.Fatalf("Failed to get openstack auth options from env: %s", err)
	}

	provider, err := openstack.AuthenticatedClient(opts)
	if err != nil {
		log.Fatalf("Failed to authenticate: %s", err)
	}

	lb, err := openstack.NewLoadBalancerV2(provider, gophercloud.EndpointOpts{})
	if err != nil {
		log.Fatalf("Failed to get octavia client: %s", err)
	}

	lbListeners := []listeners.Listener{}
	if listenerID != "" {
		l, err := listeners.Get(lb, listenerID).Extract()
		if err != nil {
			log.Fatalf("Failed to find listener %s: %s", listenerID, err)
		}
		loadBalancerID = l.Loadbalancers[0].ID
		lbListeners = append(lbListeners, *l)
	} else {
		lbListeners, err = getListeners(lb, loadBalancerID)
		if err != nil {
			log.Fatalf("Failed to get listeners of loadbalancer %s: %s", loadBalancerID, err)
		}
	}

	//Get a list of all existing pools on the lb
	lbPools, err := GetPoolsByLoadBalancer(lb, loadBalancerID)
	if err != nil {
		log.Fatalf("Failed to list pools for loadbalancer %s", err)
	}

	for _, listener := range lbListeners {
		log.Println("Processing listener", listener.Name, "id", listener.ID, "port", listener.ProtocolPort, "default pool id", listener.DefaultPoolID)
		if err != nil {
			log.Fatalf("Failed to get pools for listener %s: %w", listener.ID, err)
		}
		listenerPools := filterPoolsByListener(lbPools, listener.ID)
		poolsToMigrate := []pools.Pool{}
		for _, pool := range listenerPools {
			if pool.Protocol != targetProtocol && pool.Name != "" {
				poolsToMigrate = append(poolsToMigrate, pool)
			}
		}
		if len(poolsToMigrate) == 0 {
			log.Printf("Skipping listener %s. Nothing to migrate", listener.ID)
			continue
		} else if len(poolsToMigrate) > 1 {
			log.Printf("Warning: Skipping listener %s. Multiple pools found. I'm confused", listener.ID)
			continue
		}

		for _, pool := range poolsToMigrate {
			var newPool *pools.Pool
			for i, p := range lbPools {
				if p.Name == pool.Name && p.Protocol == targetProtocol {
					log.Printf("New pool %s with correct protocol already exists for current pool %s", p.ID, pool.ID)
					newPool = &lbPools[i]
				}
			}
			if newPool == nil {
				if newPool, err = copyPool(lb, loadBalancerID, pool, targetProtocol); err != nil {
					log.Fatalf("Failed to create a full copy of pool %s: %s", pool.ID, err)
				}
			}
			if switchDefaultPool || deleteOldPool {
				if _, err := listeners.Update(lb, listener.ID, listeners.UpdateOpts{DefaultPoolID: &newPool.ID}).Extract(); err != nil {
					log.Fatalf("Failed to update default pool: %w", err)
				}
				log.Printf("Updated default pool of listener to new pool: %s", newPool.ID)
				if _, err := waitLoadbalancerActiveProvisioningStatus(lb, loadBalancerID); err != nil {
					log.Fatalf("Error waiting for loadbalancer to become active after default pool change: %s", err)
				}
			}
			if deleteOldPool {
				if err := pools.Delete(lb, pool.ID).ExtractErr(); err != nil {
					log.Fatalf("Failed to delete old pool %s: %s", pool.ID, err)
				}
				log.Println("Deleted pool %s", pool.ID)
			}
		}
	}

}

func copyPool(lb *gophercloud.ServiceClient, loadBalancerID string, pool pools.Pool, newProtocol string) (*pools.Pool, error) {
	//get the members of the old pool
	members, err := GetMembersByPool(lb, pool.ID)
	if err != nil {
		return nil, fmt.Errorf("Failed to get members for pool %s: %w", pool.Name, err)
	}

	newPool, err := pools.Create(lb, pools.CreateOpts{
		LoadbalancerID: loadBalancerID,
		Protocol:       pools.Protocol(newProtocol),
		Name:           pool.Name,
		LBMethod:       pools.LBMethod(pool.LBMethod),
		Description:    pool.Description,
	}).Extract()
	if err != nil {
		return nil, fmt.Errorf("Failed to create new pool: %w", err)
	}

	if _, err := waitLoadbalancerActiveProvisioningStatus(lb, loadBalancerID); err != nil {
		return newPool, fmt.Errorf("Error waiting for loadbalancer to become active after pool creation: %w", err)
	}
	log.Printf("Created new pool %s", newPool.ID)

	if pool.MonitorID != "" {
		monitor, err := monitors.Get(lb, pool.MonitorID).Extract()
		if err != nil {
			return newPool, fmt.Errorf("Failed to get monitor %s for pool %s: %w", pool.MonitorID, pool.ID, err)
		}
		_, err = monitors.Create(lb, monitors.CreateOpts{
			PoolID:        newPool.ID,
			Type:          monitor.Type,
			Delay:         monitor.Delay,
			Timeout:       monitor.Timeout,
			MaxRetries:    monitor.MaxRetries,
			URLPath:       monitor.URLPath,
			HTTPMethod:    monitor.HTTPMethod,
			ExpectedCodes: monitor.ExpectedCodes,
			Name:          monitor.Name,
			AdminStateUp:  &monitor.AdminStateUp,
		}).Extract()
		if err != nil {
			return newPool, fmt.Errorf("Failed to create monitor for pool %s: %w", newPool.ID, err)
		}
		log.Printf("Create monitor for pool %s", newPool.ID)
	}
	batchMemberUpdate := make([]pools.BatchUpdateMemberOpts, 0, len(members))
	for _, member := range members {
		batchMemberUpdate = append(batchMemberUpdate, memberToBatchUpdateMemberOpts(member))

	}
	if err := pools.BatchUpdateMembers(lb, newPool.ID, batchMemberUpdate).ExtractErr(); err != nil {
		return newPool, fmt.Errorf("Failed to batch update members of new pool %s: %w", newPool.ID, err)
	}
	log.Printf("Updated members in new pool %s", newPool.ID)
	if _, err := waitLoadbalancerActiveProvisioningStatus(lb, loadBalancerID); err != nil {
		return newPool, fmt.Errorf("Error waiting for loadbalancer to become active after member update: %w", err)
	}
	return newPool, nil
}

func getListeners(c *gophercloud.ServiceClient, lbID string) ([]listeners.Listener, error) {
	page, err := listeners.List(c, listeners.ListOpts{LoadbalancerID: lbID}).AllPages()
	if err != nil {
		return nil, fmt.Errorf("Failed to list listeners for load balancer %s: %w", lbID, err)
	}
	ls, err := listeners.ExtractListeners(page)
	if err != nil {
		return nil, fmt.Errorf("Failed to extract listeners for load balancer %s: %w", lbID, err)
	}
	if len(ls) == 0 {
		return nil, errors.New("No listeners found")
	}

	return ls, nil
}

func filterPoolsByListener(pls []pools.Pool, listenerID string) []pools.Pool {
	listenerPools := []pools.Pool{}
	for _, p := range pls {
		for _, l := range p.Listeners {
			if l.ID == listenerID {
				listenerPools = append(listenerPools, p)
			}
		}
	}
	return listenerPools
}

// GetPoolsByListener finds pool for a listener.
func GetPoolsByLoadBalancer(client *gophercloud.ServiceClient, loadBalancerID string) ([]pools.Pool, error) {
	page, err := pools.List(client, pools.ListOpts{LoadbalancerID: loadBalancerID}).AllPages()
	if err != nil {
		return nil, fmt.Errorf("Failed to list pools for loadbalancer", err)
	}
	poolsList, err := pools.ExtractPools(page)

	if len(poolsList) == 0 {
		return nil, errors.New("No pools for loadbalancer found")
	}

	return poolsList, nil
}

// GetMembersbyPool get all the members in the pool.
func GetMembersByPool(client *gophercloud.ServiceClient, poolID string) ([]pools.Member, error) {
	var members []pools.Member

	err := pools.ListMembers(client, poolID, pools.ListMembersOpts{}).EachPage(func(page pagination.Page) (bool, error) {
		membersList, err := pools.ExtractMembers(page)
		if err != nil {
			return false, err
		}
		members = append(members, membersList...)

		return true, nil
	})
	if err != nil {
		return nil, err
	}

	return members, nil
}

func memberToBatchUpdateMemberOpts(member pools.Member) pools.BatchUpdateMemberOpts {
	b := pools.BatchUpdateMemberOpts{
		Address:      member.Address,
		ProtocolPort: member.ProtocolPort,
		Tags:         member.Tags,
		AdminStateUp: &member.AdminStateUp,
		Backup:       &member.Backup,
	}

	if member.Name != "" {
		b.Name = &member.Name
	}
	if member.Weight != 0 {
		b.Weight = &member.Weight
	}
	if member.SubnetID != "" {
		b.SubnetID = &member.SubnetID
	}
	if member.MonitorAddress != "" {
		b.SubnetID = &member.MonitorAddress
	}
	if member.MonitorPort > 0 {
		b.MonitorPort = &member.MonitorPort
	}
	return b
}

func waitLoadbalancerActiveProvisioningStatus(client *gophercloud.ServiceClient, loadbalancerID string) (string, error) {

	var provisioningStatus string
	err := wait.Poll(2*time.Second, 5*time.Minute, func() (bool, error) {
		loadbalancer, err := loadbalancers.Get(client, loadbalancerID).Extract()
		if err != nil {
			return false, err
		}
		provisioningStatus = loadbalancer.ProvisioningStatus
		if loadbalancer.ProvisioningStatus == "ACTIVE" {
			return true, nil
		} else if loadbalancer.ProvisioningStatus == "ERROR" {
			return true, fmt.Errorf("loadbalancer has gone into ERROR state")
		} else {
			return false, nil
		}

	})

	if err == wait.ErrWaitTimeout {
		err = fmt.Errorf("loadbalancer failed to go into ACTIVE provisioning status within allotted time")
	}
	return provisioningStatus, err
}
