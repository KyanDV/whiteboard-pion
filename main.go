package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/pion/webrtc/v3"
)

// Struktur data coretan yang akan dikirim antar client
type DrawLine struct {
	PrevX int    `json:"prevX"`
	PrevY int    `json:"prevY"`
	CurrX int    `json:"currX"`
	CurrY int    `json:"currY"`
	Color string `json:"color"`
}

// Room menyimpan daftar semua DataChannel user yang aktif
type Room struct {
	mutex   sync.RWMutex
	clients map[*webrtc.DataChannel]struct{} // Set of datachannels
}

var room = Room{
	clients: make(map[*webrtc.DataChannel]struct{}),
}

func main() {
	// 1. Serve file HTML statis (Frontend)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "index.html")
	})

	// 2. Endpoint untuk Signaling (Pertukaran Offer/Answer)
	http.HandleFunc("/sdp", handleSDP)

	fmt.Println("Server berjalan di http://localhost:8080")
	panic(http.ListenAndServe(":8080", nil))
}

func handleSDP(w http.ResponseWriter, r *http.Request) {
	// Setup Konfigurasi WebRTC (STUN Google)
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	}

	// Buat PeerConnection baru untuk setiap user yang connect
	peerConnection, err := webrtc.NewPeerConnection(config)
	if err != nil {
		http.Error(w, "Gagal membuat peer connection", http.StatusInternalServerError)
		return
	}

	// Handler jika PeerConnection menerima Data Channel baru
	peerConnection.OnDataChannel(func(d *webrtc.DataChannel) {
		fmt.Printf("User baru bergabung")

		// Saat channel terbuka, masukkan ke dalam Room
		d.OnOpen(func() {
			room.mutex.Lock()
			room.clients[d] = struct{}{}
			room.mutex.Unlock()
		})

		// Saat menerima pesan (koordinat gambar) dari satu user
		d.OnMessage(func(msg webrtc.DataChannelMessage) {
			// Broadcast pesan ini ke SEMUA user lain
			broadcast(msg.Data, d)
		})

		// Bersihkan jika close
		d.OnClose(func() {
			room.mutex.Lock()
			delete(room.clients, d)
			room.mutex.Unlock()
			fmt.Println("User disconnect")
		})
	})

	// Signaling HTTP
	// Baca Offer dari Client (JSON)
	var offer webrtc.SessionDescription
	body, _ := io.ReadAll(r.Body)
	json.Unmarshal(body, &offer)

	// Set Remote Description
	if err := peerConnection.SetRemoteDescription(offer); err != nil {
		http.Error(w, "Gagal set remote description", http.StatusInternalServerError)
		return
	}

	// Buat Answer
	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		http.Error(w, "Gagal create answer", http.StatusInternalServerError)
		return
	}

	// Set Local Description
	if err := peerConnection.SetLocalDescription(answer); err != nil {
		http.Error(w, "Gagal set local description", http.StatusInternalServerError)
		return
	}

	// Tunggu ICE Gathering selesai
	<-webrtc.GatheringCompletePromise(peerConnection)

	// Kirim balik Answer ke Client
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(peerConnection.LocalDescription())
}

// Fungsi untuk mengirim data ke semua client KECUALI pengirimnya
func broadcast(message []byte, sender *webrtc.DataChannel) {
	room.mutex.RLock()
	defer room.mutex.RUnlock()

	for client := range room.clients {
		// Pastikan channel masih open dan bukan pengirim aslinya
		if client != sender && client.ReadyState() == webrtc.DataChannelStateOpen {
			sendErr := client.SendText(string(message))
			if sendErr != nil {
				fmt.Println("Gagal kirim data:", sendErr)
			}
		}
	}
}
