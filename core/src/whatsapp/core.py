"""Main WhatsApp bot class."""

import asyncio
from typing import Any, Dict, Optional

import structlog

from ..config.settings import Settings
from .client import BridgeClient
from .models import IncomingMessage
from .orchestrator import MessageOrchestrator

logger = structlog.get_logger()


class WhatsAppBot:
    """Main bot — connects to bridge, routes messages to Claude."""

    def __init__(self, settings: Settings, dependencies: Dict[str, Any]):
        self.settings = settings
        self.deps = dependencies
        self.client: Optional[BridgeClient] = None
        self.orchestrator = MessageOrchestrator(settings, dependencies)
        self.is_running = False

    async def initialize(self) -> None:
        """Initialize bridge client."""
        if self.client is not None:
            return

        logger.info("Initializing WhatsApp bot")

        self.client = BridgeClient(
            base_url=self.settings.whatsapp_bridge_url,
            ws_url=self.settings.whatsapp_bridge_ws_url,
        )
        await self.client.connect()

        # Verify bridge is reachable
        healthy = await self.client.health_check()
        if not healthy:
            logger.warning("Bridge health check failed — will retry on WS connect")

        # Pass client to orchestrator
        self.orchestrator.set_client(self.client)

        logger.info("WhatsApp bot initialized")

    async def start(self) -> None:
        """Start listening for messages."""
        if self.is_running:
            return

        await self.initialize()

        self.is_running = True
        logger.info("WhatsApp bot starting")

        await self.client.start_listening(self._handle_message)

        # Keep alive
        while self.is_running:
            await asyncio.sleep(1)

    async def _handle_message(self, msg: IncomingMessage) -> None:
        """Route incoming message through middleware and orchestrator."""
        user = msg.user
        user_id = user.user_id
        phone = user.phone

        logger.info(
            "Message received",
            from_phone=phone,
            chat=msg.chat,
            content_type=msg.content.type,
            is_command=msg.is_command,
        )

        # Auth check
        auth_manager = self.deps.get("auth_manager")
        if auth_manager:
            allowed_phones = self.settings.allowed_phones
            if allowed_phones and phone not in allowed_phones:
                logger.warning("Unauthorized phone", phone=phone)
                await self.client.send_text(msg.chat, "Not authorized.")
                return

        # Rate limit check
        rate_limiter = self.deps.get("rate_limiter")
        if rate_limiter:
            allowed, limit_msg = await rate_limiter.check_rate_limit(user_id, 0.001)
            if not allowed:
                await self.client.send_text(msg.chat, f"Rate limited: {limit_msg}")
                return

        # Route to orchestrator
        try:
            await self.orchestrator.handle_message(msg)
        except Exception as e:
            logger.error("Message handling failed", error=str(e), phone=phone)
            await self.client.send_text(msg.chat, "Error processing message. Try again.")

    async def stop(self) -> None:
        """Graceful shutdown."""
        logger.info("Stopping WhatsApp bot")
        self.is_running = False
        if self.client:
            await self.client.disconnect()
        logger.info("WhatsApp bot stopped")

    async def health_check(self) -> bool:
        """Check health."""
        if not self.client:
            return False
        return await self.client.health_check()
