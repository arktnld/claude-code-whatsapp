"""Message orchestrator — routes WhatsApp messages to Claude.

Adapted from claude-code-telegram orchestrator for WhatsApp.
"""

import asyncio
import time
from pathlib import Path
from typing import Any, Callable, Dict, List, Optional

import structlog

from ..claude.sdk_integration import StreamUpdate
from ..config.settings import Settings
from .client import BridgeClient
from .models import IncomingMessage, OutgoingMessage
from .utils.formatting import format_for_whatsapp

logger = structlog.get_logger()


class MessageOrchestrator:
    """Routes messages to Claude and sends responses back via WhatsApp."""

    def __init__(self, settings: Settings, deps: Dict[str, Any]):
        self.settings = settings
        self.deps = deps
        self.client: Optional[BridgeClient] = None
        self._active_requests: Dict[int, asyncio.Event] = {}

    def set_client(self, client: BridgeClient) -> None:
        self.client = client

    async def handle_message(self, msg: IncomingMessage) -> None:
        """Route message based on type and content."""
        if msg.is_command:
            await self._handle_command(msg)
        elif msg.content.type == "text":
            await self._handle_text(msg)
        elif msg.content.type in ("image", "document", "audio"):
            await self._handle_media(msg)
        else:
            logger.debug("Unsupported message type", type=msg.content.type)

    async def _handle_command(self, msg: IncomingMessage) -> None:
        """Handle /commands."""
        cmd = msg.command
        args = msg.command_args
        chat = msg.chat
        user = msg.user

        if cmd == "start":
            current_dir = self.settings.approved_directory
            await self.client.send_text(
                chat,
                f"Hi {msg.push_name}! I'm your AI coding assistant.\n"
                f"Just tell me what you need.\n\n"
                f"Working in: {current_dir}/\n"
                f"Commands: /new (reset) - /status - /repo",
            )

        elif cmd == "new":
            # Reset session for this user
            self._get_user_data(user.user_id)["claude_session_id"] = None
            self._get_user_data(user.user_id)["force_new_session"] = True
            await self.client.send_text(chat, "Session reset. What's next?")

        elif cmd == "status":
            user_data = self._get_user_data(user.user_id)
            current_dir = user_data.get(
                "current_directory", str(self.settings.approved_directory)
            )
            session_id = user_data.get("claude_session_id")
            session_status = "active" if session_id else "none"
            await self.client.send_text(
                chat, f"Dir: {current_dir}\nSession: {session_status}"
            )

        elif cmd == "repo":
            await self._handle_repo(msg, args)

        elif cmd == "stop":
            interrupt = self._active_requests.get(user.user_id)
            if interrupt:
                interrupt.set()
                await self.client.send_text(chat, "Stopping...")
            else:
                await self.client.send_text(chat, "Nothing running.")

        else:
            # Unknown command — forward to Claude as text
            await self._handle_text(msg)

    async def _handle_text(self, msg: IncomingMessage) -> None:
        """Send text to Claude and respond."""
        user = msg.user
        user_id = user.user_id
        chat = msg.chat
        text = msg.text

        logger.info("Processing text", user_id=user_id, length=len(text))

        # Mark user message as read
        await self.client.mark_read(chat, msg.from_jid, msg.message_id, msg.timestamp)

        # React with hourglass to show we're processing
        await self.client.react(chat, msg.from_jid, msg.message_id, "\u23f3")

        # Typing indicator
        await self.client.send_typing(chat)

        # Send "Working..." — will be edited with final response
        progress_msg_id = await self.client.send_text(chat, "Working...")

        claude_integration = self.deps.get("claude_integration")
        if not claude_integration:
            if progress_msg_id:
                await self.client.edit_message(chat, progress_msg_id, "Claude integration not available.")
            else:
                await self.client.send_text(chat, "Claude integration not available.")
            await self.client.react(chat, msg.from_jid, msg.message_id, "")
            return

        user_data = self._get_user_data(user_id)
        current_dir = user_data.get(
            "current_directory", self.settings.approved_directory
        )
        session_id = user_data.get("claude_session_id")
        force_new = bool(user_data.get("force_new_session"))

        # Interrupt event
        interrupt_event = asyncio.Event()
        self._active_requests[user_id] = interrupt_event

        # Typing heartbeat
        heartbeat = self._start_typing_heartbeat(chat)

        try:
            claude_response = await claude_integration.run_command(
                prompt=text,
                working_directory=current_dir,
                user_id=user_id,
                session_id=session_id,
                force_new=force_new,
                interrupt_event=interrupt_event,
            )

            if force_new:
                user_data["force_new_session"] = False

            user_data["claude_session_id"] = claude_response.session_id

            # Format response
            content = claude_response.content
            if claude_response.interrupted:
                content = (content or "") + "\n\n_(Interrupted)_"

            chunks = format_for_whatsapp(content)

            # Edit "Working..." with first chunk (or full response)
            if progress_msg_id and chunks:
                await self.client.edit_message(chat, progress_msg_id, chunks[0])
                # Send remaining chunks as new messages
                for chunk in chunks[1:]:
                    await self.client.send_text(chat, chunk)
                    await asyncio.sleep(0.5)
            else:
                for chunk in chunks:
                    await self.client.send_text(chat, chunk)
                    if len(chunks) > 1:
                        await asyncio.sleep(0.5)

            # React with checkmark on success
            await self.client.react(chat, msg.from_jid, msg.message_id, "\u2705")

            # Store interaction
            storage = self.deps.get("storage")
            if storage:
                try:
                    await storage.save_claude_interaction(
                        user_id=user_id,
                        session_id=claude_response.session_id,
                        prompt=text,
                        response=claude_response,
                        ip_address=None,
                    )
                except Exception as e:
                    logger.warning("Failed to log interaction", error=str(e))

        except Exception as e:
            logger.error("Claude failed", error=str(e), user_id=user_id)
            # Edit progress to show error, react with X
            error_text = f"Error: {str(e)[:500]}"
            if progress_msg_id:
                await self.client.edit_message(chat, progress_msg_id, error_text)
            else:
                await self.client.send_text(chat, error_text)
            await self.client.react(chat, msg.from_jid, msg.message_id, "\u274c")
        finally:
            heartbeat.cancel()
            self._active_requests.pop(user_id, None)

    async def _handle_media(self, msg: IncomingMessage) -> None:
        """Handle media messages — build prompt with file content and send to Claude."""
        user = msg.user
        chat = msg.chat
        content = msg.content

        await self.client.send_typing(chat)

        if content.type == "image":
            # Read image and send as vision prompt
            import base64

            try:
                with open(content.media_url, "rb") as f:
                    image_data = base64.b64encode(f.read()).decode()
            except Exception as e:
                await self.client.send_text(chat, f"Failed to read image: {e}")
                return

            caption = content.caption or "What's in this image?"
            images = [{"data": image_data, "media_type": content.mimetype or "image/jpeg"}]

            claude_integration = self.deps.get("claude_integration")
            if not claude_integration:
                await self.client.send_text(chat, "Claude integration not available.")
                return

            user_data = self._get_user_data(user.user_id)
            current_dir = user_data.get(
                "current_directory", self.settings.approved_directory
            )
            session_id = user_data.get("claude_session_id")

            heartbeat = self._start_typing_heartbeat(chat)
            try:
                claude_response = await claude_integration.run_command(
                    prompt=caption,
                    working_directory=current_dir,
                    user_id=user.user_id,
                    session_id=session_id,
                    images=images,
                )
                user_data["claude_session_id"] = claude_response.session_id

                messages = format_for_whatsapp(claude_response.content)
                for chunk in messages:
                    await self.client.send_text(chat, chunk)
                    await asyncio.sleep(0.3)
            except Exception as e:
                await self.client.send_text(chat, f"Error: {str(e)[:500]}")
            finally:
                heartbeat.cancel()

        elif content.type == "document":
            try:
                with open(content.media_url, "r", encoding="utf-8") as f:
                    file_content = f.read()
                if len(file_content) > 50000:
                    file_content = file_content[:50000] + "\n... (truncated)"
            except UnicodeDecodeError:
                await self.client.send_text(chat, "Unsupported file format (not UTF-8).")
                return
            except Exception as e:
                await self.client.send_text(chat, f"Failed to read file: {e}")
                return

            caption = content.caption or "Review this file:"
            prompt = (
                f"{caption}\n\n*File:* {content.filename}\n\n```\n{file_content}\n```"
            )
            # Reuse text handler with constructed prompt
            fake_msg = IncomingMessage(
                type="message",
                from_jid=msg.from_jid,
                chat=msg.chat,
                message_id=msg.message_id,
                timestamp=msg.timestamp,
                push_name=msg.push_name,
                content=type(content)(type="text", text=prompt),
            )
            await self._handle_text(fake_msg)

        elif content.type == "audio":
            await self.client.send_text(
                chat, "Audio transcription not yet supported. Send text instead."
            )

    async def _handle_repo(self, msg: IncomingMessage, args: list) -> None:
        """Handle /repo command."""
        chat = msg.chat
        user_data = self._get_user_data(msg.user.user_id)
        base = self.settings.approved_directory

        if args:
            target = base / args[0]
            if not target.is_dir():
                await self.client.send_text(chat, f"Not found: {args[0]}")
                return
            user_data["current_directory"] = target
            user_data["claude_session_id"] = None
            is_git = (target / ".git").is_dir()
            git_badge = " (git)" if is_git else ""
            await self.client.send_text(
                chat, f"Switched to {args[0]}/{git_badge}"
            )
            return

        # List repos
        try:
            entries = sorted(
                [d for d in base.iterdir() if d.is_dir() and not d.name.startswith(".")],
                key=lambda d: d.name,
            )
        except OSError as e:
            await self.client.send_text(chat, f"Error: {e}")
            return

        if not entries:
            await self.client.send_text(chat, f"No repos in {base}")
            return

        # Use poll for interactive repo selection (max 12 options in WhatsApp)
        repo_names = [d.name for d in entries[:12]]
        if len(entries) <= 12:
            await self.client.create_poll(
                chat=chat,
                question="Switch to project:",
                options=repo_names,
                max_selections=1,
            )
        else:
            # Too many repos for poll, fallback to text list
            current_dir = user_data.get("current_directory", base)
            current_name = current_dir.name if current_dir != base else None
            lines = []
            for d in entries:
                is_git = (d / ".git").is_dir()
                icon = "git" if is_git else "dir"
                marker = " <--" if d.name == current_name else ""
                lines.append(f"[{icon}] {d.name}/{marker}")
            await self.client.send_text(
                chat, "*Repos*\n\n" + "\n".join(lines) + "\n\nUse: /repo <name>"
            )

    # --- User data store (in-memory, per user_id) ---

    _user_data: Dict[int, Dict[str, Any]] = {}

    def _get_user_data(self, user_id: int) -> Dict[str, Any]:
        if user_id not in self._user_data:
            self._user_data[user_id] = {
                "current_directory": self.settings.approved_directory,
                "claude_session_id": None,
                "force_new_session": False,
            }
        return self._user_data[user_id]

    def _start_typing_heartbeat(self, chat: str, interval: float = 3.0) -> asyncio.Task:
        async def _heartbeat():
            try:
                while True:
                    await asyncio.sleep(interval)
                    await self.client.send_typing(chat)
            except asyncio.CancelledError:
                pass
        return asyncio.create_task(_heartbeat())
