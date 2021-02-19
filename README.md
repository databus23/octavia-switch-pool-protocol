octavia pool protocol switcher
==============================

This repo contains a small script to switch the default pool of loadbalancers listeners.
As changing the pool protocol is not allowed by the ocatvia api a new pool is created for each listener.

Usage
======
Usage of ./octavia-switch-pool-protocol:
  -delete
    	Delete the old pool (implies --switch-default-pool)
  -listenerID string
    	Modify the pools of the given listener
  -loadBalancerID string
    	Modify the pools of all listeners of t he given loadbalancer
  -protocol string
    	The protocol to which the pools should be changed to (default "PROXY")
  -switch-default-pool
    	Change the default pool of the listner to the new pool

Example
=============

1. Step just create the new pools, don't change the default pool yet:

```
>  go run . -loadBalancerID=8baae870-b04c-4502-b1fe-f820da6d160a -protocol=PROXY
2021/02/19 11:34:55 Processing listener listener_0_kube_service_s-qa-de-1_kube-system_kube-system-nginx-ingress-controller id 5aa556b4-bae2-471a-86a6-eca9b20e65b7 port 80 default pool id 2f1379b7-97d3-4e1a-982a-6b44afee7e34
2021/02/19 11:35:02 Created new pool 506c28aa-3d8a-4fa2-8aab-9a424dfb76c2
2021/02/19 11:35:03 Create monitor for pool 506c28aa-3d8a-4fa2-8aab-9a424dfb76c2
2021/02/19 11:35:16 Updated members in new pool 506c28aa-3d8a-4fa2-8aab-9a424dfb76c2
2021/02/19 11:35:26 Processing listener listener_1_kube_service_s-qa-de-1_kube-system_kube-system-nginx-ingress-controller id d2ff2da3-67cf-466a-b61e-31d77a1ed378 port 443 default pool id c223029f-e99c-4123-a33c-4b8880d0dfa1
2021/02/19 11:35:34 Created new pool 1b958dd5-51fd-4436-8caa-04490fe5759c
2021/02/19 11:35:35 Create monitor for pool 1b958dd5-51fd-4436-8caa-04490fe5759c
2021/02/19 11:35:45 Updated members in new pool 1b958dd5-51fd-4436-8caa-04490fe5759c

```

2. After the new pool has setteled down and the receiving backend has been configured to accept proxy protocol

```
> go run . -loadBalancerID=8baae870-b04c-4502-b1fe-f820da6d160a -protocol=PROXY -switch-default-pool
2021/02/19 13:51:49 Processing listener listener_0_kube_service_s-qa-de-1_kube-system_kube-system-nginx-ingress-controller id 5aa556b4-bae2-471a-86a6-eca9b20e65b7 port 80 default pool id 2f1379b7-97d3-4e1a-982a-6b44afee7e34
2021/02/19 13:51:49 New pool 506c28aa-3d8a-4fa2-8aab-9a424dfb76c2 with correct protocol already exists for current pool 2f1379b7-97d3-4e1a-982a-6b44afee7e34
2021/02/19 13:51:49 Updated default pool of listener to new pool: 506c28aa-3d8a-4fa2-8aab-9a424dfb76c2
2021/02/19 13:51:55 Processing listener listener_1_kube_service_s-qa-de-1_kube-system_kube-system-nginx-ingress-controller id d2ff2da3-67cf-466a-b61e-31d77a1ed378 port 443 default pool id c223029f-e99c-4123-a33c-4b8880d0dfa1
2021/02/19 13:51:55 New pool 1b958dd5-51fd-4436-8caa-04490fe5759c with correct protocol already exists for current pool c223029f-e99c-4123-a33c-4b8880d0dfa1
2021/02/19 13:51:56 Updated default pool of listener to new pool: 1b958dd5-51fd-4436-8caa-04490fe5759c

```

