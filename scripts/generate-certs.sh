#!/bin/bash

# Script to generate certificates for Cloudless mTLS authentication
# Generates: CA certificate, coordinator certificate, and agent certificates

set -e

# Configuration
CERTS_DIR="${CERTS_DIR:-./deployments/docker/certs}"
VALIDITY_DAYS="${VALIDITY_DAYS:-365}"
KEY_SIZE="${KEY_SIZE:-4096}"

# Certificate subject information
COUNTRY="US"
STATE="California"
LOCALITY="San Francisco"
ORG="Cloudless"
ORG_UNIT="Platform"

echo "=================================================="
echo "Cloudless Certificate Generation"
echo "=================================================="
echo ""

# Create certificates directory
mkdir -p "${CERTS_DIR}"
cd "${CERTS_DIR}"

echo "Generating certificates in: $(pwd)"
echo ""

# Step 1: Generate CA private key and certificate
echo "[1/5] Generating CA private key and certificate..."
if [ ! -f "ca.key" ] || [ ! -f "ca.crt" ]; then
    # Generate CA private key
    openssl genrsa -out ca.key ${KEY_SIZE}

    # Generate CA certificate
    openssl req -new -x509 -days ${VALIDITY_DAYS} -key ca.key -out ca.crt \
        -subj "/C=${COUNTRY}/ST=${STATE}/L=${LOCALITY}/O=${ORG}/OU=${ORG_UNIT}/CN=Cloudless CA"

    echo "✓ CA certificate generated"
else
    echo "✓ CA certificate already exists"
fi
echo ""

# Step 2: Generate Coordinator certificate
echo "[2/5] Generating coordinator certificate..."
if [ ! -f "coordinator.key" ] || [ ! -f "coordinator.crt" ]; then
    # Generate coordinator private key
    openssl genrsa -out coordinator.key ${KEY_SIZE}

    # Generate coordinator CSR
    openssl req -new -key coordinator.key -out coordinator.csr \
        -subj "/C=${COUNTRY}/ST=${STATE}/L=${LOCALITY}/O=${ORG}/OU=${ORG_UNIT}/CN=coordinator"

    # Create SAN config for coordinator
    cat > coordinator.ext <<EOF
authorityKeyIdentifier=keyid,issuer
basicConstraints=CA:FALSE
keyUsage = digitalSignature, nonRepudiation, keyEncipherment, dataEncipherment
subjectAltName = @alt_names

[alt_names]
DNS.1 = coordinator
DNS.2 = coordinator.cloudless.local
DNS.3 = localhost
IP.1 = 127.0.0.1
IP.2 = 172.20.0.10
EOF

    # Sign coordinator certificate with CA
    openssl x509 -req -in coordinator.csr -CA ca.crt -CAkey ca.key \
        -CAcreateserial -out coordinator.crt -days ${VALIDITY_DAYS} \
        -extfile coordinator.ext

    # Clean up
    rm coordinator.csr coordinator.ext

    echo "✓ Coordinator certificate generated"
else
    echo "✓ Coordinator certificate already exists"
fi
echo ""

# Step 3: Generate Agent-1 certificate
echo "[3/5] Generating agent-1 certificate..."
if [ ! -f "agent-1.key" ] || [ ! -f "agent-1.crt" ]; then
    openssl genrsa -out agent-1.key ${KEY_SIZE}

    openssl req -new -key agent-1.key -out agent-1.csr \
        -subj "/C=${COUNTRY}/ST=${STATE}/L=${LOCALITY}/O=${ORG}/OU=${ORG_UNIT}/CN=agent-1"

    cat > agent-1.ext <<EOF
authorityKeyIdentifier=keyid,issuer
basicConstraints=CA:FALSE
keyUsage = digitalSignature, nonRepudiation, keyEncipherment, dataEncipherment
subjectAltName = @alt_names

[alt_names]
DNS.1 = agent-1
DNS.2 = agent1
DNS.3 = localhost
IP.1 = 127.0.0.1
IP.2 = 172.20.0.11
EOF

    openssl x509 -req -in agent-1.csr -CA ca.crt -CAkey ca.key \
        -CAcreateserial -out agent-1.crt -days ${VALIDITY_DAYS} \
        -extfile agent-1.ext

    rm agent-1.csr agent-1.ext

    echo "✓ Agent-1 certificate generated"
else
    echo "✓ Agent-1 certificate already exists"
fi
echo ""

# Step 4: Generate Agent-2 certificate
echo "[4/5] Generating agent-2 certificate..."
if [ ! -f "agent-2.key" ] || [ ! -f "agent-2.crt" ]; then
    openssl genrsa -out agent-2.key ${KEY_SIZE}

    openssl req -new -key agent-2.key -out agent-2.csr \
        -subj "/C=${COUNTRY}/ST=${STATE}/L=${LOCALITY}/O=${ORG}/OU=${ORG_UNIT}/CN=agent-2"

    cat > agent-2.ext <<EOF
authorityKeyIdentifier=keyid,issuer
basicConstraints=CA:FALSE
keyUsage = digitalSignature, nonRepudiation, keyEncipherment, dataEncipherment
subjectAltName = @alt_names

[alt_names]
DNS.1 = agent-2
DNS.2 = agent2
DNS.3 = localhost
IP.1 = 127.0.0.1
IP.2 = 172.20.0.12
EOF

    openssl x509 -req -in agent-2.csr -CA ca.crt -CAkey ca.key \
        -CAcreateserial -out agent-2.crt -days ${VALIDITY_DAYS} \
        -extfile agent-2.ext

    rm agent-2.csr agent-2.ext

    echo "✓ Agent-2 certificate generated"
else
    echo "✓ Agent-2 certificate already exists"
fi
echo ""

# Step 5: Generate Agent-3 certificate
echo "[5/5] Generating agent-3 certificate..."
if [ ! -f "agent-3.key" ] || [ ! -f "agent-3.crt" ]; then
    openssl genrsa -out agent-3.key ${KEY_SIZE}

    openssl req -new -key agent-3.key -out agent-3.csr \
        -subj "/C=${COUNTRY}/ST=${STATE}/L=${LOCALITY}/O=${ORG}/OU=${ORG_UNIT}/CN=agent-3"

    cat > agent-3.ext <<EOF
authorityKeyIdentifier=keyid,issuer
basicConstraints=CA:FALSE
keyUsage = digitalSignature, nonRepudiation, keyEncipherment, dataEncipherment
subjectAltName = @alt_names

[alt_names]
DNS.1 = agent-3
DNS.2 = agent3
DNS.3 = localhost
IP.1 = 127.0.0.1
IP.2 = 172.20.0.13
EOF

    openssl x509 -req -in agent-3.csr -CA ca.crt -CAkey ca.key \
        -CAcreateserial -out agent-3.crt -days ${VALIDITY_DAYS} \
        -extfile agent-3.ext

    rm agent-3.csr agent-3.ext

    echo "✓ Agent-3 certificate generated"
else
    echo "✓ Agent-3 certificate already exists"
fi
echo ""

# Set appropriate permissions
echo "Setting file permissions..."
chmod 600 *.key
chmod 644 *.crt
echo "✓ Permissions set"
echo ""

# Summary
echo "=================================================="
echo "Certificate Generation Complete!"
echo "=================================================="
echo ""
echo "Generated certificates:"
echo "  - CA Certificate:          ca.crt"
echo "  - CA Private Key:          ca.key"
echo "  - Coordinator Certificate: coordinator.crt"
echo "  - Coordinator Private Key: coordinator.key"
echo "  - Agent-1 Certificate:     agent-1.crt"
echo "  - Agent-1 Private Key:     agent-1.key"
echo "  - Agent-2 Certificate:     agent-2.crt"
echo "  - Agent-2 Private Key:     agent-2.key"
echo "  - Agent-3 Certificate:     agent-3.crt"
echo "  - Agent-3 Private Key:     agent-3.key"
echo ""
echo "Certificate validity: ${VALIDITY_DAYS} days"
echo "Location: ${CERTS_DIR}"
echo ""
echo "You can now start the Cloudless cluster with: make compose-up"
echo ""
