#!/bin/bash
echo "=== Iniciando Proxy SSH para Cloud Run (VPS Externo) ==="

# Variables de entorno
export DHOST=${DHOST:-"garcia.mrctz.live"}
export DPORT=${DPORT:-"90"}
export PORT=${PORT:-"8080"}
export PACKSKIP=${PACKSKIP:-"0"}
export UDPGW_PORT=${UDPGW_PORT:-"7300"}

# Variables para autenticación SSH (opcionales)
export SSH_USER=${SSH_USER:-""}
export SSH_PASSWORD=${SSH_PASSWORD:-""}
export SSH_KEY_PATH=${SSH_KEY_PATH:-""}

echo "[INFO] Configuración del VPS externo:"
echo "  - Host: $DHOST"
echo "  - Puerto SSH: $DPORT"
if [ -n "$SSH_USER" ]; then
    echo "  - Usuario SSH: $SSH_USER"
else
    echo "  - Usuario SSH: [No especificado - se usará el del cliente]"
fi
echo "  - Proxy puerto: $PORT"
echo "  - UDPgw puerto: $UDPGW_PORT"
echo "  - Paquetes a saltar: $PACKSKIP"

# Función para verificar conectividad SSH
check_ssh_connection() {
    echo "[INFO] Verificando conectividad SSH a $DHOST:$DPORT..."
    
    # Solo verificar conectividad TCP básica, no autenticación SSH específica
    timeout 10 bash -c "echo >/dev/tcp/$DHOST/$DPORT" 2>/dev/null
    local result=$?
    
    if [ $result -eq 0 ]; then
        echo "[INFO] ✓ Puerto SSH accesible en $DHOST:$DPORT"
        return 0
    else
        echo "[WARNING] ✗ No se pudo conectar a $DHOST:$DPORT"
        return 1
    fi
}

# Configurar SSH si hay clave privada
if [ -n "$SSH_PRIVATE_KEY" ]; then
    echo "[INFO] Configurando clave SSH privada..."
    echo "$SSH_PRIVATE_KEY" > /root/.ssh/id_rsa
    chmod 600 /root/.ssh/id_rsa
    export SSH_KEY_PATH="/root/.ssh/id_rsa"
fi

# Verificar conexión SSH
check_ssh_connection

# Iniciar UDPgw local
echo "[INFO] Iniciando Badvpn UDPgw en puerto $UDPGW_PORT..."
tmux new-session -d -s udpgw_session "/usr/local/bin/badvpn-udpgw --listen-addr 127.0.0.1:$UDPGW_PORT --max-clients 1000 --max-connections-for-client 10"
sleep 2
if netstat -tuln | grep -q ":$UDPGW_PORT "; then
    echo "[INFO] ✓ UDPgw corriendo en puerto $UDPGW_PORT"
else
    echo "[WARNING] ✗ UDPgw no se inició correctamente en puerto $UDPGW_PORT"
fi
echo ""
echo "=== INFORMACIÓN DE CONEXIÓN ==="
echo "Host destino: $DHOST:$DPORT"
echo "Modo: Proxy TCP transparente (sin autenticación previa)"
echo "UDPgw local: 127.0.0.1:$UDPGW_PORT"
echo "Proxy HTTP: 0.0.0.0:$PORT"
echo "Nota: Las credenciales SSH las proporciona el cliente SSH"
echo "================================"
echo ""

echo "[INFO] Iniciando proxy en primer plano..."
exec /usr/local/bin/proxy
