"""WhatsApp message models."""

from dataclasses import dataclass
from typing import Optional


@dataclass
class WhatsAppUser:
    """WhatsApp user identified by phone JID."""

    jid: str  # e.g. 5511999999999@s.whatsapp.net
    push_name: str = ""

    @property
    def phone(self) -> str:
        return self.jid.split("@")[0]

    @property
    def user_id(self) -> int:
        """Numeric ID derived from phone for compatibility with telegram core."""
        return int(self.phone) % (2**31)


@dataclass
class MessageContent:
    """Content of a WhatsApp message."""

    type: str  # text, image, document, audio
    text: str = ""
    caption: str = ""
    media_url: str = ""
    mimetype: str = ""
    filename: str = ""


@dataclass
class IncomingMessage:
    """Message received from WhatsApp bridge."""

    type: str
    from_jid: str
    chat: str
    message_id: str
    timestamp: int
    push_name: str
    content: MessageContent

    @property
    def user(self) -> WhatsAppUser:
        return WhatsAppUser(jid=self.from_jid, push_name=self.push_name)

    @property
    def text(self) -> str:
        return self.content.text or self.content.caption or ""

    @property
    def is_command(self) -> bool:
        return self.text.startswith("/")

    @property
    def command(self) -> Optional[str]:
        if not self.is_command:
            return None
        return self.text.split()[0][1:].lower()

    @property
    def command_args(self) -> list:
        if not self.is_command:
            return []
        return self.text.split()[1:]


@dataclass
class OutgoingMessage:
    """Message to send via WhatsApp bridge."""

    to: str
    type: str = "text"
    text: str = ""
    media_path: str = ""
    caption: str = ""
