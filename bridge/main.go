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

// SendResponse wraps message ID from a sent message
type SendResponse struct {
	MessageID string `json:"message_id"`
}

// SendMessage sends a message via WhatsApp and returns the message ID
func (b *Bridge) SendMessage(msg OutgoingMessage) (SendResponse, error) {
	jid, err := types.ParseJID(msg.To)
	if err != nil {
		return SendResponse{}, fmt.Errorf("invalid JID %s: %w", msg.To, err)
	}

	ctx := context.Background()
	var resp whatsmeow.SendResponse

	switch msg.Type {
	case "text":
		resp, err = b.client.SendMessage(ctx, jid, &waE2E.Message{
			Conversation: proto.String(msg.Text),
		})
	case "image":
		data, readErr := os.ReadFile(msg.MediaPath)
		if readErr != nil {
			return SendResponse{}, fmt.Errorf("failed to read image: %w", readErr)
		}
		uploaded, uploadErr := b.client.Upload(ctx, data, whatsmeow.MediaImage)
		if uploadErr != nil {
			return SendResponse{}, fmt.Errorf("failed to upload image: %w", uploadErr)
		}
		resp, err = b.client.SendMessage(ctx, jid, &waE2E.Message{
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
			return SendResponse{}, fmt.Errorf("failed to read document: %w", readErr)
		}
		uploaded, uploadErr := b.client.Upload(ctx, data, whatsmeow.MediaDocument)
		if uploadErr != nil {
			return SendResponse{}, fmt.Errorf("failed to upload document: %w", uploadErr)
		}
		resp, err = b.client.SendMessage(ctx, jid, &waE2E.Message{
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
		return SendResponse{}, fmt.Errorf("unsupported message type: %s", msg.Type)
	}

	if err != nil {
		return SendResponse{}, err
	}
	return SendResponse{MessageID: resp.ID}, nil
}

// EditMessage edits a previously sent message
func (b *Bridge) EditMessage(chatJID, messageID, newText string) error {
	jid, err := types.ParseJID(chatJID)
	if err != nil {
		return fmt.Errorf("invalid JID: %w", err)
	}
	editMsg := b.client.BuildEdit(jid, messageID, &waE2E.Message{
		Conversation: proto.String(newText),
	})
	_, err = b.client.SendMessage(context.Background(), jid, editMsg)
	return err
}

// ReactToMessage sends a reaction emoji to a message
func (b *Bridge) ReactToMessage(chatJID, senderJID, messageID, reaction string) error {
	chat, err := types.ParseJID(chatJID)
	if err != nil {
		return fmt.Errorf("invalid chat JID: %w", err)
	}
	sender, err := types.ParseJID(senderJID)
	if err != nil {
		return fmt.Errorf("invalid sender JID: %w", err)
	}
	reactMsg := b.client.BuildReaction(chat, sender, messageID, reaction)
	_, err = b.client.SendMessage(context.Background(), chat, reactMsg)
	return err
}

// RevokeMessage deletes a previously sent message
func (b *Bridge) RevokeMessage(chatJID, messageID string) error {
	chat, err := types.ParseJID(chatJID)
	if err != nil {
		return fmt.Errorf("invalid JID: %w", err)
	}
	_, err = b.client.RevokeMessage(context.Background(), chat, messageID)
	return err
}

// MarkRead marks messages as read
func (b *Bridge) MarkRead(chatJID, senderJID, messageID string, timestamp int64) error {
	chat, err := types.ParseJID(chatJID)
	if err != nil {
		return fmt.Errorf("invalid chat JID: %w", err)
	}
	sender, err := types.ParseJID(senderJID)
	if err != nil {
		return fmt.Errorf("invalid sender JID: %w", err)
	}
	ts := time.Unix(timestamp, 0)
	return b.client.MarkRead(context.Background(), []types.MessageID{messageID}, ts, chat, sender)
}

// CreatePoll sends a poll message
func (b *Bridge) CreatePoll(chatJID, question string, options []string, maxSelections int) (SendResponse, error) {
	jid, err := types.ParseJID(chatJID)
	if err != nil {
		return SendResponse{}, fmt.Errorf("invalid JID: %w", err)
	}
	if maxSelections <= 0 {
		maxSelections = 1
	}
	pollMsg := b.client.BuildPollCreation(question, options, maxSelections)
	resp, err := b.client.SendMessage(context.Background(), jid, pollMsg)
	if err != nil {
		return SendResponse{}, err
	}
	return SendResponse{MessageID: resp.ID}, nil
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
		resp, err := b.SendMessage(msg)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "sent", "message_id": resp.MessageID})
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
		b.client.SendChatPresence(context.Background(), jid, types.ChatPresenceComposing, types.ChatPresenceMediaText)
		json.NewEncoder(w).Encode(map[string]string{"status": "typing"})
	})

	// Edit a previously sent message
	mux.HandleFunc("/edit", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Chat      string `json:"chat"`
			MessageID string `json:"message_id"`
			Text      string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := b.EditMessage(req.Chat, req.MessageID, req.Text); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "edited"})
	})

	// React to a message
	mux.HandleFunc("/react", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Chat      string `json:"chat"`
			Sender    string `json:"sender"`
			MessageID string `json:"message_id"`
			Reaction  string `json:"reaction"` // emoji, empty string to remove
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := b.ReactToMessage(req.Chat, req.Sender, req.MessageID, req.Reaction); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "reacted"})
	})

	// Revoke (delete) a sent message
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Chat      string `json:"chat"`
			MessageID string `json:"message_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := b.RevokeMessage(req.Chat, req.MessageID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "revoked"})
	})

	// Mark message as read
	mux.HandleFunc("/read", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Chat      string `json:"chat"`
			Sender    string `json:"sender"`
			MessageID string `json:"message_id"`
			Timestamp int64  `json:"timestamp"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := b.MarkRead(req.Chat, req.Sender, req.MessageID, req.Timestamp); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "read"})
	})

	// Create a poll
	mux.HandleFunc("/poll", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Chat          string   `json:"chat"`
			Question      string   `json:"question"`
			Options       []string `json:"options"`
			MaxSelections int      `json:"max_selections"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp, err := b.CreatePoll(req.Chat, req.Question, req.Options, req.MaxSelections)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "sent", "message_id": resp.MessageID})
	})

	// Pair via phone code (no QR needed)
	mux.HandleFunc("/pair-phone", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Phone string `json:"phone"` // e.g. +5511999999999
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		code, err := b.client.PairPhone(context.Background(), req.Phone, true, whatsmeow.PairClientChrome, "Claude Code WhatsApp")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "pairing", "code": code})
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
