#!/usr/bin/env bash
set -e

export cluster_id=""
export access_key=""
export access_secret=""
# vpc or eni-multi-ip
export category=""
export region=""

usage() {
	echo "usage: $0 --cluster-id <cluster-id> --access-key <access-key> --access-secret <access-key-secret> \
--region <region> --category <category:[vpc|eni-multi-ip]> --image <terway image>"
}

install_env() {
	if [ -z ${cluster_id} ] || [ -z ${access_key} ] || [ -z ${access_secret} ] || [ -z ${category} ]; then
		echo "invaild argument"
		usage
		exit 1;
	fi

	if [ -z "${terway_image}" ]; then
		export terway_image="registry.aliyuncs.com/acs/terway:v1.0.9.10-gfc1045e-aliyun"
	fi

	export ACCESS_KEY_ID=${access_key}
	export ACCESS_KEY_SECRET=${access_secret}
	export REGION=${region}
	temp_dir=$(mktemp -d)
	export temp_dir
	aliyun_cluster=$(aliyun cs GET /clusters/${cluster_id})
	export aliyun_cluster
	security_group=$(echo "${aliyun_cluster}" | jq .security_group_id | tr -d '"')
	export security_group
	vswitch=$(echo "${aliyun_cluster}" | jq .vswitch_id | tr -d '"')
	export vswitch
	aliyun cs GET /k8s/"${cluster_id}"/user_config | jq -r .config > "${temp_dir}"/kubeconfig.yaml
	export KUBECONFIG=${temp_dir}/kubeconfig.yaml
	service_cidr=$(aliyun cs GET /clusters/"${cluster_id}" | jq .parameters.ServiceCIDR | tr -d '"')
	export service_cidr
	pod_cidr=$(aliyun cs GET /clusters/"${cluster_id}" | jq .parameters.ContainerCIDR | tr -d '"')
	export pod_cidr

	if ! kubectl get ds terway -n kube-system; then
		echo "invaild kubeconfig for cluster or not a terway cluster"
		exit 1;
	fi

	install_terway
}

install_terway() {
	terway_template=""
	case ${category} in
		vpc)
			terway_template='terway-vpc.yml'
			;;
		eni-multi-ip)
			terway_template='terway-multiip.yml'
			;;
		eni-only)
			terway_template='terway-eni-only.yml'
			;;
		*)
			echo "invaild category "${category}
			exit 1;
	esac
	cp templates/terway/${terway_template} "${temp_dir}"/
    sed -e "s#ACCESS_KEY#${access_key}#g" \
	    -e "s#ACCESS_SECRET#${access_secret}#g" \
	    -e "s#SERVICE_CIDR#${service_cidr}#g" \
	    -e "s#SECURITY_GROUP#${security_group}#g" \
	    -e "s#VSWITCH#${vswitch}#g" \
	    -e "s#POD_CIDR#${pod_cidr}#g" \
	    -e "s#TERWAY_IMAGE#${terway_image}#g" \
	    -i "${temp_dir}/${terway_template}"

	kubectl apply -f "${temp_dir}/${terway_template}"
	kubectl delete pod -n kube-system -l app=terway
	sleep 30
}

while [[ $# -ge 1 ]]
do
    key=$1
    shift
    case "$key" in
        --cluster-id)
            export cluster_id=$1
            shift
            ;;
        --access-key)
            export access_key=$1
            shift
            ;;
        --access-secret)
            export access_secret=$1
            shift
            ;;
        --region)
            export region=$1
            shift
            ;;
        --category)
            export category=$1
            shift
            ;;
        --image)
            export terway_image=$1
            shift
            ;;
        *)
            usage
            exit 1
            ;;
    esac
done

install_env
