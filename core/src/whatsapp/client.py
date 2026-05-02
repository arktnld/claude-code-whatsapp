"""HTTP + WebSocket client for WhatsApp bridge."""

import asyncio
import json
from typing import Any, Callable, Dict, List, Optional

import aiohttp
import structlog

from .models import IncomingMessage, MessageContent, OutgoingMessage

logger = structlog.get_logger()


class BridgeClient:
    """Communicates with the Go WhatsApp bridge via HTTP and WebSocket."""

    def __init__(self, base_url: str, ws_url: str):
        self.base_url = base_url.rstrip("/")
        self.ws_url = ws_url.rstrip("/")
        self._session: Optional[aiohttp.ClientSession] = None
        self._ws: Optional[aiohttp.ClientWebSocketResponse] = None
        self._on_message: Optional[Callable] = None
        self._ws_task: Optional[asyncio.Task] = None
        self._reconnect = True

    async def connect(self) -> None:
        """Initialize HTTP session."""
        self._session = aiohttp.ClientSession()
        logger.info("Bridge client connected", base_url=self.base_url)

    async def disconnect(self) -> None:
        """Close connections."""
        self._reconnect = False
        if self._ws_task:
            self._ws_task.cancel()
            try:
                await self._ws_task
            except asyncio.CancelledError:
                pass
        if self._ws and not self._ws.closed:
            await self._ws.close()
        if self._session:
            await self._session.close()
        logger.info("Bridge client disconnected")

    async def health_check(self) -> bool:
        """Check bridge health."""
        try:
            async with self._session.get(f"{self.base_url}/health") as resp:
                return resp.status == 200
        except Exception:
            return False

    async def get_status(self) -> Dict[str, Any]:
        """Get bridge connection status."""
        async with self._session.get(f"{self.base_url}/status") as resp:
            return await resp.json()

    async def send_message(self, msg: OutgoingMessage) -> Optional[str]:
        """Send message via bridge. Returns message_id or None."""
        payload = {
            "to": msg.to,
            "type": msg.type,
            "text": msg.text,
            "media_path": msg.media_path,
            "caption": msg.caption,
        }
        try:
            async with self._session.post(
                f"{self.base_url}/send", json=payload
            ) as resp:
                if resp.status == 200:
                    data = await resp.json()
                    return data.get("message_id")
                return None
        except Exception as e:
            logger.error("Failed to send message", error=str(e))
            return None

    async def send_text(self, to: str, text: str) -> Optional[str]:
        """Send text message. Returns message_id."""
        return await self.send_message(OutgoingMessage(to=to, type="text", text=text))

    async def edit_message(self, chat: str, message_id: str, new_text: str) -> bool:
        """Edit a previously sent message."""
        try:
            async with self._session.post(
                f"{self.base_url}/edit",
                json={"chat": chat, "message_id": message_id, "text": new_text},
            ) as resp:
                return resp.status == 200
        except Exception as e:
            logger.error("Failed to edit message", error=str(e))
            return False

    async def react(self, chat: str, sender: str, message_id: str, reaction: str) -> bool:
        """React to a message with an emoji. Empty string removes reaction."""
        try:
            async with self._session.post(
                f"{self.base_url}/react",
                json={
                    "chat": chat,
                    "sender": sender,
                    "message_id": message_id,
                    "reaction": reaction,
                },
            ) as resp:
                return resp.status == 200
        except Exception as e:
            logger.error("Failed to react", error=str(e))
            return False

    async def revoke_message(self, chat: str, message_id: str) -> bool:
        """Delete a previously sent message."""
        try:
            async with self._session.post(
                f"{self.base_url}/revoke",
                json={"chat": chat, "message_id": message_id},
            ) as resp:
                return resp.status == 200
        except Exception as e:
            logger.error("Failed to revoke message", error=str(e))
            return False

    async def mark_read(
        self, chat: str, sender: str, message_id: str, timestamp: int
    ) -> bool:
        """Mark a message as read."""
        try:
            async with self._session.post(
                f"{self.base_url}/read",
                json={
                    "chat": chat,
                    "sender": sender,
                    "message_id": message_id,
                    "timestamp": timestamp,
                },
            ) as resp:
                return resp.status == 200
        except Exception as e:
            logger.error("Failed to mark read", error=str(e))
            return False

    async def create_poll(
        self, chat: str, question: str, options: List[str], max_selections: int = 1
    ) -> Optional[str]:
        """Create a poll. Returns message_id."""
        try:
            async with self._session.post(
                f"{self.base_url}/poll",
                json={
                    "chat": chat,
                    "question": question,
                    "options": options,
                    "max_selections": max_selections,
                },
            ) as resp:
                if resp.status == 200:
                    data = await resp.json()
                    return data.get("message_id")
                return None
        except Exception as e:
            logger.error("Failed to create poll", error=str(e))
            return None

    async def send_typing(self, to: str) -> None:
        """Send typing indicator."""
        try:
            async with self._session.post(
                f"{self.base_url}/typing", json={"to": to}
            ) as resp:
                pass
        except Exception:
            pass

    async def start_listening(self, on_message: Callable) -> None:
        """Start WebSocket listener for incoming messages."""
        self._on_message = on_message
        self._reconnect = True
        self._ws_task = asyncio.create_task(self._ws_loop())

    async def _ws_loop(self) -> None:
        """WebSocket reconnect loop."""
        while self._reconnect:
            try:
                logger.info("Connecting to bridge WebSocket", url=self.ws_url)
                self._ws = await self._session.ws_connect(self.ws_url)
                logger.info("WebSocket connected")

                async for ws_msg in self._ws:
                    if ws_msg.type == aiohttp.WSMsgType.TEXT:
                        try:
                            data = json.loads(ws_msg.data)
                            msg = self._parse_message(data)
                            if msg and self._on_message:
                                await self._on_message(msg)
                        except Exception as e:
                            logger.error("Failed to process WS message", error=str(e))
                    elif ws_msg.type == aiohttp.WSMsgType.ERROR:
                        logger.error("WS error", error=str(self._ws.exception()))
                        break

            except asyncio.CancelledError:
                break
            except Exception as e:
                logger.error("WS connection failed", error=str(e))

            if self._reconnect:
                logger.info("Reconnecting in 5s...")
                await asyncio.sleep(5)

    @staticmethod
    def _parse_message(data: Dict[str, Any]) -> Optional[IncomingMessage]:
        """Parse raw JSON into IncomingMessage."""
        try:
            content_data = data.get("content", {})
            content = MessageContent(
                type=content_data.get("type", "text"),
                text=content_data.get("text", ""),
                caption=content_data.get("caption", ""),
                media_url=content_data.get("media_url", ""),
                mimetype=content_data.get("mimetype", ""),
                filename=content_data.get("filename", ""),
            )
            return IncomingMessage(
                type=data.get("type", "message"),
                from_jid=data.get("from", ""),
                chat=data.get("chat", ""),
                message_id=data.get("message_id", ""),
                timestamp=data.get("timestamp", 0),
                push_name=data.get("push_name", ""),
                content=content,
            )
        except Exception:
            return None
