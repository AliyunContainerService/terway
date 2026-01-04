#!/bin/bash
# =============================================================================
# Enterprise Security Group Creation Script for Terway E2E Testing
# =============================================================================
#
# This script creates two enterprise security groups for security group
# connectivity testing in Terway E2E tests.
#
# Prerequisites:
# - aliyun CLI installed and configured
# - VPC ID where the security groups will be created
#
# Usage:
#   ./create_enterprise_security_groups.sh --region <region> --vpc <vpc-id>
#
# Example:
#   ./create_enterprise_security_groups.sh --region cn-hangzhou --vpc vpc-xxx
#
# After running this script, use the output to set the test configuration:
#   export TERWAY_SG_TEST_CONFIG="TestSecurityGroup_TrunkMode:<client-sg-id>:<server-sg-id>"
# =============================================================================

set -e

# Default values
REGION=""
VPC_ID=""
RESOURCE_GROUP_ID=""
CLIENT_SG_NAME="terway-e2e-client-sg"
SERVER_SG_NAME="terway-e2e-server-sg"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

print_usage() {
    echo "Usage: $0 --region <region> --vpc <vpc-id> [--resource-group <resource-group-id>]"
    echo ""
    echo "Required arguments:"
    echo "  --region          Aliyun region (e.g., cn-hangzhou)"
    echo "  --vpc             VPC ID where security groups will be created"
    echo ""
    echo "Optional arguments:"
    echo "  --resource-group  Resource group ID (optional)"
    echo "  --client-sg-name  Client security group name (default: terway-e2e-client-sg)"
    echo "  --server-sg-name  Server security group name (default: terway-e2e-server-sg)"
    echo "  --help            Show this help message"
}

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --region)
            REGION="$2"
            shift 2
            ;;
        --vpc)
            VPC_ID="$2"
            shift 2
            ;;
        --resource-group)
            RESOURCE_GROUP_ID="$2"
            shift 2
            ;;
        --client-sg-name)
            CLIENT_SG_NAME="$2"
            shift 2
            ;;
        --server-sg-name)
            SERVER_SG_NAME="$2"
            shift 2
            ;;
        --help)
            print_usage
            exit 0
            ;;
        *)
            echo -e "${RED}Unknown argument: $1${NC}"
            print_usage
            exit 1
            ;;
    esac
done

# Validate required arguments
if [[ -z "$REGION" ]]; then
    echo -e "${RED}Error: --region is required${NC}"
    print_usage
    exit 1
fi

if [[ -z "$VPC_ID" ]]; then
    echo -e "${RED}Error: --vpc is required${NC}"
    print_usage
    exit 1
fi

echo -e "${GREEN}=== Creating Enterprise Security Groups for Terway E2E Testing ===${NC}"
echo "Region: $REGION"
echo "VPC ID: $VPC_ID"
echo ""

# Build common arguments
COMMON_ARGS="--RegionId $REGION"
if [[ -n "$RESOURCE_GROUP_ID" ]]; then
    COMMON_ARGS="$COMMON_ARGS --ResourceGroupId $RESOURCE_GROUP_ID"
fi

# =============================================================================
# Create Client Security Group
# - Allows egress TCP port 80 to all destinations
# - Allows ingress TCP port 80 from private IP ranges
# =============================================================================

echo -e "${YELLOW}Step 1: Creating client security group...${NC}"

CLIENT_SG_RESULT=$(aliyun ecs CreateSecurityGroup \
    $COMMON_ARGS \
    --VpcId "$VPC_ID" \
    --SecurityGroupName "$CLIENT_SG_NAME" \
    --SecurityGroupType enterprise \
    --Description "Terway E2E test - Client SG: egress 80 allowed, ingress 80 from private" \
    --output cols=SecurityGroupId)

CLIENT_SG_ID=$(echo "$CLIENT_SG_RESULT" | tail -n 1 | tr -d ' ')

if [[ -z "$CLIENT_SG_ID" ]] || [[ "$CLIENT_SG_ID" == "SecurityGroupId" ]]; then
    echo -e "${RED}Failed to create client security group${NC}"
    exit 1
fi

echo -e "${GREEN}Created client security group: $CLIENT_SG_ID${NC}"

# Add egress rule for port 80
echo "Adding egress rule: allow TCP port 80 to all..."
aliyun ecs AuthorizeSecurityGroupEgress \
    --RegionId "$REGION" \
    --SecurityGroupId "$CLIENT_SG_ID" \
    --IpProtocol tcp \
    --PortRange "80/80" \
    --DestCidrIp "0.0.0.0/0" \
    --Policy accept \
    --Description "Allow egress TCP port 80" \
    > /dev/null

# Add ingress rules for port 80 from private IP ranges
echo "Adding ingress rule: allow TCP port 80 from 10.0.0.0/8..."
aliyun ecs AuthorizeSecurityGroup \
    --RegionId "$REGION" \
    --SecurityGroupId "$CLIENT_SG_ID" \
    --IpProtocol tcp \
    --PortRange "80/80" \
    --SourceCidrIp "10.0.0.0/8" \
    --Policy accept \
    --Description "Allow ingress TCP port 80 from 10.0.0.0/8" \
    > /dev/null

echo "Adding ingress rule: allow TCP port 80 from 172.16.0.0/12..."
aliyun ecs AuthorizeSecurityGroup \
    --RegionId "$REGION" \
    --SecurityGroupId "$CLIENT_SG_ID" \
    --IpProtocol tcp \
    --PortRange "80/80" \
    --SourceCidrIp "172.16.0.0/12" \
    --Policy accept \
    --Description "Allow ingress TCP port 80 from 172.16.0.0/12" \
    > /dev/null

echo "Adding ingress rule: allow TCP port 80 from 192.168.0.0/16..."
aliyun ecs AuthorizeSecurityGroup \
    --RegionId "$REGION" \
    --SecurityGroupId "$CLIENT_SG_ID" \
    --IpProtocol tcp \
    --PortRange "80/80" \
    --SourceCidrIp "192.168.0.0/16" \
    --Policy accept \
    --Description "Allow ingress TCP port 80 from 192.168.0.0/16" \
    > /dev/null

echo -e "${GREEN}✓ Client security group configured${NC}"
echo ""

# =============================================================================
# Create Server Security Group
# - Allows ingress TCP port 80 from private IP ranges
# - Denies egress TCP port 80 to private IP ranges
# =============================================================================

echo -e "${YELLOW}Step 2: Creating server security group...${NC}"

SERVER_SG_RESULT=$(aliyun ecs CreateSecurityGroup \
    $COMMON_ARGS \
    --VpcId "$VPC_ID" \
    --SecurityGroupName "$SERVER_SG_NAME" \
    --SecurityGroupType enterprise \
    --Description "Terway E2E test - Server SG: ingress 80 allowed, egress 80 denied to private" \
    --output cols=SecurityGroupId)

SERVER_SG_ID=$(echo "$SERVER_SG_RESULT" | tail -n 1 | tr -d ' ')

if [[ -z "$SERVER_SG_ID" ]] || [[ "$SERVER_SG_ID" == "SecurityGroupId" ]]; then
    echo -e "${RED}Failed to create server security group${NC}"
    # Cleanup client SG
    aliyun ecs DeleteSecurityGroup --RegionId "$REGION" --SecurityGroupId "$CLIENT_SG_ID" > /dev/null 2>&1 || true
    exit 1
fi

echo -e "${GREEN}Created server security group: $SERVER_SG_ID${NC}"

# Add ingress rules for port 80 from private IP ranges
echo "Adding ingress rule: allow TCP port 80 from 10.0.0.0/8..."
aliyun ecs AuthorizeSecurityGroup \
    --RegionId "$REGION" \
    --SecurityGroupId "$SERVER_SG_ID" \
    --IpProtocol tcp \
    --PortRange "80/80" \
    --SourceCidrIp "10.0.0.0/8" \
    --Policy accept \
    --Description "Allow ingress TCP port 80 from 10.0.0.0/8" \
    > /dev/null

echo "Adding ingress rule: allow TCP port 80 from 172.16.0.0/12..."
aliyun ecs AuthorizeSecurityGroup \
    --RegionId "$REGION" \
    --SecurityGroupId "$SERVER_SG_ID" \
    --IpProtocol tcp \
    --PortRange "80/80" \
    --SourceCidrIp "172.16.0.0/12" \
    --Policy accept \
    --Description "Allow ingress TCP port 80 from 172.16.0.0/12" \
    > /dev/null

echo "Adding ingress rule: allow TCP port 80 from 192.168.0.0/16..."
aliyun ecs AuthorizeSecurityGroup \
    --RegionId "$REGION" \
    --SecurityGroupId "$SERVER_SG_ID" \
    --IpProtocol tcp \
    --PortRange "80/80" \
    --SourceCidrIp "192.168.0.0/16" \
    --Policy accept \
    --Description "Allow ingress TCP port 80 from 192.168.0.0/16" \
    > /dev/null

# Add egress deny rules for port 80 to private IP ranges
echo "Adding egress rule: deny TCP port 80 to 10.0.0.0/8..."
aliyun ecs AuthorizeSecurityGroupEgress \
    --RegionId "$REGION" \
    --SecurityGroupId "$SERVER_SG_ID" \
    --IpProtocol tcp \
    --PortRange "80/80" \
    --DestCidrIp "10.0.0.0/8" \
    --Policy drop \
    --Description "Deny egress TCP port 80 to 10.0.0.0/8" \
    > /dev/null

echo "Adding egress rule: deny TCP port 80 to 172.16.0.0/12..."
aliyun ecs AuthorizeSecurityGroupEgress \
    --RegionId "$REGION" \
    --SecurityGroupId "$SERVER_SG_ID" \
    --IpProtocol tcp \
    --PortRange "80/80" \
    --DestCidrIp "172.16.0.0/12" \
    --Policy drop \
    --Description "Deny egress TCP port 80 to 172.16.0.0/12" \
    > /dev/null

echo "Adding egress rule: deny TCP port 80 to 192.168.0.0/16..."
aliyun ecs AuthorizeSecurityGroupEgress \
    --RegionId "$REGION" \
    --SecurityGroupId "$SERVER_SG_ID" \
    --IpProtocol tcp \
    --PortRange "80/80" \
    --DestCidrIp "192.168.0.0/16" \
    --Policy drop \
    --Description "Deny egress TCP port 80 to 192.168.0.0/16" \
    > /dev/null

echo -e "${GREEN}✓ Server security group configured${NC}"
echo ""

# =============================================================================
# Output Summary
# =============================================================================

echo -e "${GREEN}=== Security Groups Created Successfully ===${NC}"
echo ""
echo "Client Security Group:"
echo "  ID: $CLIENT_SG_ID"
echo "  Name: $CLIENT_SG_NAME"
echo "  Rules:"
echo "    - Egress:  ALLOW TCP port 80 to all (0.0.0.0/0)"
echo "    - Ingress: ALLOW TCP port 80 from private IP ranges"
echo ""
echo "Server Security Group:"
echo "  ID: $SERVER_SG_ID"
echo "  Name: $SERVER_SG_NAME"
echo "  Rules:"
echo "    - Ingress: ALLOW TCP port 80 from private IP ranges"
echo "    - Egress:  DENY  TCP port 80 to private IP ranges"
echo ""
echo "Private IP ranges: 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16"
echo ""
echo "Test scenario:"
echo "  - Client -> Server (port 80): SHOULD SUCCEED"
echo "    (Client egress allowed, Server ingress allowed)"
echo "  - Server -> Client (port 80): SHOULD FAIL"
echo "    (Server egress denied to private IPs)"
echo ""
echo -e "${YELLOW}=== Test Configuration ===${NC}"
echo ""
echo "To use these security groups in the Terway E2E test, set the environment variable:"
echo ""
echo -e "${GREEN}export TERWAY_SG_TEST_CONFIG=\"TestSecurityGroup_TrunkMode:$CLIENT_SG_ID:$SERVER_SG_ID\"${NC}"
echo ""
echo "Then run the test:"
echo ""
echo -e "${GREEN}go test -count=1 -v -tags e2e ./tests -run TestSecurityGroup_TrunkMode${NC}"
echo ""

# =============================================================================
# Cleanup Script
# =============================================================================

echo -e "${YELLOW}=== Cleanup (run after testing) ===${NC}"
echo ""
echo "To delete the security groups after testing:"
echo ""
echo "aliyun ecs DeleteSecurityGroup --RegionId $REGION --SecurityGroupId $CLIENT_SG_ID"
echo "aliyun ecs DeleteSecurityGroup --RegionId $REGION --SecurityGroupId $SERVER_SG_ID"
echo ""
