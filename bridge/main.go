package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	_ "github.com/mattn/go-sqlite3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// IncomingMessage represents a message received from WhatsApp
type IncomingMessage struct {
	Type      string         `json:"type"`
	From      string         `json:"from"`
	Chat      string         `json:"chat"`
	MessageID string         `json:"message_id"`
	Timestamp int64          `json:"timestamp"`
	PushName  string         `json:"push_name"`
	Content   MessageContent `json:"content"`
}

// MessageContent holds the content of a message
type MessageContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Caption  string `json:"caption,omitempty"`
	MediaURL string `json:"media_url,omitempty"`
	MimeType string `json:"mimetype,omitempty"`
	Filename string `json:"filename,omitempty"`
}

// OutgoingMessage represents a message to send via WhatsApp
type OutgoingMessage struct {
	To        string `json:"to"`
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	MediaPath string `json:"media_path,omitempty"`
	Caption   string `json:"caption,omitempty"`
}

// Bridge manages WhatsApp connection and HTTP/WS API
type Bridge struct {
	client    *whatsmeow.Client
	upgrader  websocket.Upgrader
	wsClients map[*websocket.Conn]bool
	wsMu      sync.RWMutex
	dbPath    string
}

// NewBridge creates a new Bridge instance
func NewBridge(dbPath string) *Bridge {
	return &Bridge{
		dbPath: dbPath,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		wsClients: make(map[*websocket.Conn]bool),
	}
}

// Connect initializes WhatsApp connection
func (b *Bridge) Connect() error {
	container, err := sqlstore.New("sqlite3", fmt.Sprintf("file:%s?_foreign_keys=on", b.dbPath), nil)
	if err != nil {
		return fmt.Errorf("failed to create store: %w", err)
	}

	deviceStore, err := container.GetFirstDevice()
	if err != nil {
		return fmt.Errorf("failed to get device: %w", err)
	}

	b.client = whatsmeow.NewClient(deviceStore, nil)
	b.client.AddEventHandler(b.eventHandler)

	if b.client.Store.ID == nil {
		// No session, need QR code
		qrChan, _ := b.client.GetQRChannel(context.Background())
		err = b.client.Connect()
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}

		for evt := range qrChan {
			switch evt.Event {
			case "code":
				fmt.Println("\n=== SCAN QR CODE ===")
				fmt.Println(evt.Code)
				fmt.Println("====================\n")
			case "success":
				log.Println("Login successful!")
			case "timeout":
				return fmt.Errorf("QR code timeout")
			}
		}
	} else {
		err = b.client.Connect()
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		log.Println("Connected with existing session")
	}

	return nil
}

// eventHandler processes WhatsApp events
func (b *Bridge) eventHandler(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		b.handleMessage(v)
	case *events.Connected:
		log.Println("WhatsApp connected")
	case *events.Disconnected:
		log.Println("WhatsApp disconnected")
	case *events.LoggedOut:
		log.Println("WhatsApp logged out")
	}
}

// handleMessage processes incoming WhatsApp messages
func (b *Bridge) handleMessage(msg *events.Message) {
	// Skip messages from self
	if msg.Info.IsFromMe {
		return
	}

	// Skip group messages — only accept private (1:1) chats
	if msg.Info.Chat.Server == "g.us" || msg.Info.Chat.Server == "broadcast" {
		return
	}

	incoming := IncomingMessage{
		Type:      "message",
		From:      msg.Info.Sender.String(),
		Chat:      msg.Info.Chat.String(),
		MessageID: msg.Info.ID,
		Timestamp: msg.Info.Timestamp.Unix(),
		PushName:  msg.Info.PushName,
	}

	switch {
	case msg.Message.GetConversation() != "":
		incoming.Content = MessageContent{
			Type: "text",
			Text: msg.Message.GetConversation(),
		}
	case msg.Message.GetExtendedTextMessage() != nil:
		incoming.Content = MessageContent{
			Type: "text",
			Text: msg.Message.GetExtendedTextMessage().GetText(),
		}
	case msg.Message.GetImageMessage() != nil:
		imgMsg := msg.Message.GetImageMessage()
		data, err := b.client.Download(imgMsg)
		if err != nil {
			log.Printf("Failed to download image: %v", err)
			return
		}
		path := fmt.Sprintf("/tmp/claude-whatsapp/media/%s.jpg", msg.Info.ID)
		os.MkdirAll("/tmp/claude-whatsapp/media", 0755)
		if err := os.WriteFile(path, data, 0644); err != nil {
			log.Printf("Failed to save image: %v", err)
			return
		}
		incoming.Content = MessageContent{
			Type:     "image",
			Caption:  imgMsg.GetCaption(),
			MediaURL: path,
			MimeType: imgMsg.GetMimetype(),
		}
	case msg.Message.GetDocumentMessage() != nil:
		docMsg := msg.Message.GetDocumentMessage()
		data, err := b.client.Download(docMsg)
		if err != nil {
			log.Printf("Failed to download document: %v", err)
			return
		}
		filename := docMsg.GetFileName()
		path := fmt.Sprintf("/tmp/claude-whatsapp/media/%s_%s", msg.Info.ID, filename)
		os.MkdirAll("/tmp/claude-whatsapp/media", 0755)
		if err := os.WriteFile(path, data, 0644); err != nil {
			log.Printf("Failed to save document: %v", err)
			return
		}
		incoming.Content = MessageContent{
			Type:     "document",
			Caption:  docMsg.GetCaption(),
			MediaURL: path,
			MimeType: docMsg.GetMimetype(),
			Filename: filename,
		}
	case msg.Message.GetAudioMessage() != nil:
		audioMsg := msg.Message.GetAudioMessage()
		data, err := b.client.Download(audioMsg)
		if err != nil {
			log.Printf("Failed to download audio: %v", err)
			return
		}
		path := fmt.Sprintf("/tmp/claude-whatsapp/media/%s.ogg", msg.Info.ID)
		os.MkdirAll("/tmp/claude-whatsapp/media", 0755)
		if err := os.WriteFile(path, data, 0644); err != nil {
			log.Printf("Failed to save audio: %v", err)
			return
		}
		incoming.Content = MessageContent{
			Type:     "audio",
			MediaURL: path,
			MimeType: audioMsg.GetMimetype(),
		}
	default:
		// Unsupported message type
		return
	}

	b.broadcastToWS(incoming)
}

// broadcastToWS sends message to all WebSocket clients
func (b *Bridge) broadcastToWS(msg IncomingMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Failed to marshal message: %v", err)
		return
	}

	b.wsMu.RLock()
	defer b.wsMu.RUnlock()

	for conn := range b.wsClients {
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			log.Printf("Failed to write to WS client: %v", err)
		}
	}
}

// SendMessage sends a message via WhatsApp
func (b *Bridge) SendMessage(msg OutgoingMessage) error {
	jid, err := types.ParseJID(msg.To)
	if err != nil {
		return fmt.Errorf("invalid JID %s: %w", msg.To, err)
	}

	ctx := context.Background()

	switch msg.Type {
	case "text":
		_, err = b.client.SendMessage(ctx, jid, &waE2E.Message{
			Conversation: proto.String(msg.Text),
		})
	case "image":
		data, readErr := os.ReadFile(msg.MediaPath)
		if readErr != nil {
			return fmt.Errorf("failed to read image: %w", readErr)
		}
		uploaded, uploadErr := b.client.Upload(ctx, data, whatsmeow.MediaImage)
		if uploadErr != nil {
			return fmt.Errorf("failed to upload image: %w", uploadErr)
		}
		_, err = b.client.SendMessage(ctx, jid, &waE2E.Message{
			ImageMessage: &waE2E.ImageMessage{
				Caption:       proto.String(msg.Caption),
				URL:           proto.String(uploaded.URL),
				DirectPath:    proto.String(uploaded.DirectPath),
				MediaKey:      uploaded.MediaKey,
				Mimetype:      proto.String("image/jpeg"),
				FileEncSHA256: uploaded.FileEncSHA256,
				FileSHA256:    uploaded.FileSHA256,
				FileLength:    proto.Uint64(uint64(len(data))),
			},
		})
	case "document":
		data, readErr := os.ReadFile(msg.MediaPath)
		if readErr != nil {
			return fmt.Errorf("failed to read document: %w", readErr)
		}
		uploaded, uploadErr := b.client.Upload(ctx, data, whatsmeow.MediaDocument)
		if uploadErr != nil {
			return fmt.Errorf("failed to upload document: %w", uploadErr)
		}
		_, err = b.client.SendMessage(ctx, jid, &waE2E.Message{
			DocumentMessage: &waE2E.DocumentMessage{
				Caption:       proto.String(msg.Caption),
				URL:           proto.String(uploaded.URL),
				DirectPath:    proto.String(uploaded.DirectPath),
				MediaKey:      uploaded.MediaKey,
				Mimetype:      proto.String(msg.Caption),
				FileName:      proto.String(msg.Caption),
				FileEncSHA256: uploaded.FileEncSHA256,
				FileSHA256:    uploaded.FileSHA256,
				FileLength:    proto.Uint64(uint64(len(data))),
			},
		})
	default:
		return fmt.Errorf("unsupported message type: %s", msg.Type)
	}

	return err
}

// SetupHTTP configures HTTP routes
func (b *Bridge) SetupHTTP() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		connected := b.client != nil && b.client.IsConnected()
		json.NewEncoder(w).Encode(map[string]interface{}{
			"connected": connected,
			"timestamp": time.Now().Unix(),
		})
	})

	mux.HandleFunc("/send", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var msg OutgoingMessage
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := b.SendMessage(msg); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "sent"})
	})

	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := b.upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("WS upgrade failed: %v", err)
			return
		}

		b.wsMu.Lock()
		b.wsClients[conn] = true
		b.wsMu.Unlock()

		log.Println("WS client connected")

		defer func() {
			b.wsMu.Lock()
			delete(b.wsClients, conn)
			b.wsMu.Unlock()
			conn.Close()
			log.Println("WS client disconnected")
		}()

		// Keep connection alive, read pings
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				break
			}
		}
	})

	mux.HandleFunc("/typing", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			To string `json:"to"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		jid, err := types.ParseJID(req.To)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		b.client.SendChatPresence(jid, types.ChatPresenceComposing, types.ChatPresenceMediaText)
		json.NewEncoder(w).Encode(map[string]string{"status": "typing"})
	})

	return mux
}

func main() {
	port := os.Getenv("BRIDGE_PORT")
	if port == "" {
		port = "8080"
	}
	dbPath := os.Getenv("BRIDGE_DB_PATH")
	if dbPath == "" {
		dbPath = "whatsapp.db"
	}

	bridge := NewBridge(dbPath)

	log.Println("Connecting to WhatsApp...")
	if err := bridge.Connect(); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}

	server := &http.Server{
		Addr:    ":" + port,
		Handler: bridge.SetupHTTP(),
	}

	// Graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan

		log.Println("Shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		server.Shutdown(ctx)
		if bridge.client != nil {
			bridge.client.Disconnect()
		}
	}()

	log.Printf("Bridge listening on :%s", port)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("HTTP server error: %v", err)
	}
}
