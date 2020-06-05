#!/usr/bin/env bats
load helpers

function delete_deploy() {
	retry 20 2 object_not_exist svc tomcat-service
	retry 20 2 object_not_exist svc nginx-service
	retry 20 2 object_not_exist pod -l app=vpc-samehost-nginx
	retry 20 2 object_not_exist pod -l app=vpc-samehost-tomcat
	retry 20 2 object_not_exist pod -l app=vpc-crosshost-nginx
	retry 20 2 object_not_exist pod -l app=vpc-crosshost-tomcat
	retry 20 2 object_not_exist pod -l app=vpc-tomcat
	retry 20 2 object_not_exist pod -l app=vpc-eni-nginx
	retry 20 2 object_not_exist pod -l app=eni-tomcat
	retry 20 2 object_not_exist pod -l app=eni-nginx
	sleep 10
}

function setup() {
	kubectl delete -f templates/testcases/network_connection/samehost.yml || true
	kubectl delete -f templates/testcases/network_connection/crosshost.yml || true
	kubectl delete -f templates/testcases/network_connection/eni.yml || true
	kubectl delete -f templates/testcases/network_connection/vpc-eni.yml || true
	delete_deploy || true
}

function request_hostport {
	run "kubectl get pod -n kube-system -l app=terway -o name | cut -d '/' -f 2 | xargs -n1 -I {} kubectl exec -it {} -n kube-system curl 127.0.0.1:$1"
	if [[ "$status" -eq 0 ]]; then
		return 0
	fi
	false
	echo "node port result: "$result
}

@test "pod connection same host" {
	retry 5 5 kubectl apply -f templates/testcases/network_connection/samehost.yml

	retry 20 2 object_exist svc tomcat-service
	retry 20 2 object_exist svc nginx-service
	retry 20 5 pod_running pod -l app=vpc-samehost-tomcat
	retry 20 5 object_exist pod -l app=vpc-samehost-nginx
	# wait nginx startup
	sleep 20
	retry 3 2 request_hostport 30080
	[ "$status" -eq "0" ]
	kubectl delete -f templates/testcases/network_connection/samehost.yml || true
	delete_deploy
}

@test "pod connection cross host" {
	retry 5 5 kubectl apply -f templates/testcases/network_connection/crosshost.yml
    retry 20 2 object_exist svc tomcat-service
    retry 20 2 object_exist svc nginx-service
    retry 20 5 pod_running pod -l app=vpc-crosshost-tomcat
    retry 20 5 object_exist pod -l app=vpc-crosshost-nginx
	# wait nginx startup
    sleep 20
    retry 3 2 request_hostport 30090
    [ "$status" -eq "0" ]
    kubectl delete -f templates/testcases/network_connection/crosshost.yml || true
    delete_deploy
}

@test "pod connection eni" {
	if [ "$category" = "vpc" ]; then
		retry 5 5 kubectl apply -f templates/testcases/network_connection/eni.yml
		retry 20 2 object_exist svc tomcat-service
		retry 20 2 object_exist svc nginx-service
		retry 20 5 pod_running pod -l app=eni-tomcat
		retry 20 5 object_exist pod -l app=eni-nginx
		# wait nginx startup
        sleep 20
	    retry 3 2 request_hostport 30100
	    [ "$status" -eq "0" ]
	    kubectl delete -f templates/testcases/network_connection/eni.yml || true
	    delete_deploy
    fi
}

@test "pod connection vpc -> eni" {
	if [ "$category" = "vpc" ]; then
		retry 5 5 kubectl apply -f templates/testcases/network_connection/vpc-eni.yml
		retry 20 2 object_exist svc tomcat-service
		retry 20 2 object_exist svc nginx-service
		retry 20 5 pod_running pod -l app=vpc-tomcat
		retry 20 5 object_exist pod -l app=vpc-eni-nginx
		# wait nginx startup
        sleep 20
	    retry 3 2 request_hostport 30110
	    [ "$status" -eq "0" ]
	    kubectl delete -f templates/testcases/network_connection/vpc-eni.yml || true
	    delete_deploy
    fi
}
