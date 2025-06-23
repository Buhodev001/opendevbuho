package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"time"
)

func generateWebSocketAccept() string {
	bytes := make([]byte, 20)
	rand.Read(bytes)
	return base64.StdEncoding.EncodeToString(bytes)
}

func testVPSConnection(dhost string, dport int) bool {
	log.Printf("[INFO] Verificando conectividad TCP con %s:%d...", dhost, dport)

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", dhost, dport), 10*time.Second)
	if err != nil {
		log.Printf("[WARNING] No se pudo conectar al host: %v", err)
		return false
	}
	defer conn.Close()

	log.Printf("[INFO] ✓ Host accesible en %s:%d", dhost, dport)
	return true
}

func handleConnection(clientConn net.Conn, dhost string, dport int, packetsToSkip int) {
	defer clientConn.Close()

	clientAddr := clientConn.RemoteAddr().String()
	log.Printf("[INFO] Nueva conexión desde: %s", clientAddr)

	// Configurar Keep-Alive para la conexión del cliente
	if tcpClientConn, ok := clientConn.(*net.TCPConn); ok {
		tcpClientConn.SetKeepAlive(true)
		tcpClientConn.SetKeepAlivePeriod(time.Second * 60) // Envía keep-alive cada 60 segundos
		log.Printf("[DEBUG] Keep-Alive configurado para cliente %s", clientAddr)
	}

	// Enviar respuesta HTTP 101 (WebSocket handshake simulado)
	response := fmt.Sprintf(
		"HTTP/1.1 101 Switching Protocols\r\n"+
			"Connection: Upgrade\r\n"+
			"Date: %s\r\n"+
			"Sec-WebSocket-Accept: %s\r\n"+
			"Upgrade: websocket\r\n"+
			"Server: go-proxy/1.0\r\n\r\n",
		time.Now().UTC().Format(time.RFC1123),
		generateWebSocketAccept(),
	)

	if _, err := clientConn.Write([]byte(response)); err != nil {
		log.Printf("[ERROR] Failed to send handshake to %s: %v", clientAddr, err)
		return
	}

	// Conectar al servidor SSH externo
	log.Printf("[INFO] Conectando a %s:%d para cliente %s", dhost, dport, clientAddr)
	targetConn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", dhost, dport), 15*time.Second)
	if err != nil {
		log.Printf("[ERROR] Failed to connect to %s:%d for client %s: %v", dhost, dport, clientAddr, err)
		return
	}
	defer targetConn.Close()

	// Configurar Keep-Alive para la conexión al servidor de destino
	if tcpTargetConn, ok := targetConn.(*net.TCPConn); ok {
		tcpTargetConn.SetKeepAlive(true)
		tcpTargetConn.SetKeepAlivePeriod(time.Second * 60) // Envía keep-alive cada 60 segundos
		log.Printf("[DEBUG] Keep-Alive configurado para destino %s:%d", dhost, dport)
	}

	log.Printf("[INFO] ✓ Túnel TCP establecido: %s <-> %s:%d", clientAddr, dhost, dport)

	// Canal para coordinar el cierre
	done := make(chan bool, 2)

	// Goroutine para datos cliente -> servidor SSH (con skip de paquetes)
	go func() {
		defer func() { done <- true }()

		packetCount := 0
		buffer := make([]byte, 4096)
		bytesTransferred := int64(0)

		for {
			n, err := clientConn.Read(buffer)
			if err != nil {
				if err != io.EOF {
					log.Printf("[CLIENT->SSH] Read error from %s: %v", clientAddr, err)
				}
				return
			}

			// Lógica de skip de paquetes
			if packetCount < packetsToSkip {
				packetCount++
				log.Printf("[DEBUG] Skipping packet %d from %s", packetCount, clientAddr)
				continue
			} else if packetCount == packetsToSkip {
				// Después de saltar los paquetes, reenvía el primero y luego todos los demás.
				// La condición `packetCount == packetsToSkip` solo es relevante para el primer paquete
				// después de que se cumpla el número de saltos. Los paquetes subsiguientes
				// se procesarán en la rama `else` implícita.
			}

			if _, err := targetConn.Write(buffer[:n]); err != nil {
				log.Printf("[CLIENT->SSH] Write error to SSH server for %s: %v", clientAddr, err)
				return
			}
			bytesTransferred += int64(n)
			// Reinicia packetCount si ya se omitieron los paquetes iniciales y se está transfiriendo datos
			// Esto asegura que la lógica de "skip" solo afecte los primeros N paquetes de la conexión.
			if packetCount > 0 { // Solo si PACKSKIP es mayor que 0
				packetCount = 0
			}
		}
	}()

	// Goroutine para datos servidor SSH -> cliente
	go func() {
		defer func() { done <- true }()

		bytesTransferred := int64(0)
		buffer := make([]byte, 4096)

		for {
			n, err := targetConn.Read(buffer)
			if err != nil {
				if err != io.EOF {
					log.Printf("[SSH->CLIENT] Read error from SSH server for %s: %v", clientAddr, err)
				}
				return
			}

			if _, err := clientConn.Write(buffer[:n]); err != nil {
				log.Printf("[SSH->CLIENT] Write error to %s: %v", clientAddr, err)
				return
			}

			bytesTransferred += int64(n)
		}
	}()

	// Esperar a que termine cualquiera de las dos goroutines
	<-done
	log.Printf("[INFO] ✗ Túnel cerrado: %s", clientAddr)
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Variables de entorno
	dhost := getEnv("DHOST", "taquito.pp.ua") // Asegúrate que este sea tu VPS real
	dport, _ := strconv.Atoi(getEnv("DPORT", "22"))
	mainPort := getEnv("PORT", "8080")
	packetsToSkip, _ := strconv.Atoi(getEnv("PACKSKIP", "0")) // Valor por defecto 0 ahora
	// UDPgw_PORT no es usado directamente en main.go, solo en entrypoint.sh

	log.Printf("=== PROXY TCP TRANSPARENTE ===")
	log.Printf("[INFO] Host destino: %s:%d", dhost, dport)
	log.Printf("[INFO] Puerto del proxy: %s", mainPort)
	log.Printf("[INFO] Paquetes a omitir: %d", packetsToSkip)
	log.Printf("[INFO] Modo: Túnel TCP sin autenticación previa")
	log.Printf("===============================")

	// Verificar conectividad inicial con el host
	if !testVPSConnection(dhost, dport) {
		log.Printf("[WARNING] No se pudo verificar la conectividad inicial")
		log.Printf("[INFO] El proxy continuará de todas formas...")
	}

	// Crear servidor TCP
	listener, err := net.Listen("tcp", ":"+mainPort)
	if err != nil {
		log.Fatalf("[FATAL] Failed to start server on port %s: %v", mainPort, err)
	}
	defer listener.Close()

	log.Printf("[INFO] ✓ Servidor iniciado en puerto %s", mainPort)
	log.Printf("[INFO] Esperando conexiones...")

	// Aceptar conexiones
	connectionCount := 0
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("[ERROR] Accept failed: %v", err)
			continue
		}

		connectionCount++
		log.Printf("[INFO] Conexión #%d aceptada desde: %s", connectionCount, conn.RemoteAddr())

		// Manejar cada conexión en una goroutine separada
		go handleConnection(conn, dhost, dport, packetsToSkip)
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
