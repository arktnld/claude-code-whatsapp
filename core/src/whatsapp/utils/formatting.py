"""WhatsApp message formatting utilities.

WhatsApp supports limited formatting:
- *bold*
- _italic_
- ~strikethrough~
- ```monospace```
- No HTML, no inline keyboards
"""

import re
from typing import List

# Max WhatsApp message length
MAX_MESSAGE_LENGTH = 4096


def format_for_whatsapp(text: str) -> List[str]:
    """Convert Claude markdown response to WhatsApp-friendly format and split if needed."""
    if not text:
        return ["(empty response)"]

    formatted = _convert_markdown(text)
    return _split_message(formatted)


def _convert_markdown(text: str) -> str:
    """Convert standard markdown to WhatsApp markdown."""
    result = text

    # Headers: ## Title -> *Title*
    result = re.sub(r"^#{1,6}\s+(.+)$", r"*\1*", result, flags=re.MULTILINE)

    # Bold: **text** -> *text*
    result = re.sub(r"\*\*(.+?)\*\*", r"*\1*", result)

    # Inline code: `code` -> ```code```
    # But skip if already in a code block
    result = re.sub(r"(?<!`)(`(?!`))(.+?)(`(?!`))", r"```\2```", result)

    # HTML tags (from telegram formatting) -> strip
    result = re.sub(r"</?[^>]+>", "", result)

    return result.strip()


def _split_message(text: str) -> List[str]:
    """Split long messages at natural boundaries."""
    if len(text) <= MAX_MESSAGE_LENGTH:
        return [text]

    chunks = []
    remaining = text

    while remaining:
        if len(remaining) <= MAX_MESSAGE_LENGTH:
            chunks.append(remaining)
            break

        # Find split point — prefer double newline, then single, then space
        chunk = remaining[:MAX_MESSAGE_LENGTH]
        split_at = chunk.rfind("\n\n")
        if split_at == -1 or split_at < MAX_MESSAGE_LENGTH // 2:
            split_at = chunk.rfind("\n")
        if split_at == -1 or split_at < MAX_MESSAGE_LENGTH // 2:
            split_at = chunk.rfind(" ")
        if split_at == -1:
            split_at = MAX_MESSAGE_LENGTH

        chunks.append(remaining[:split_at].rstrip())
        remaining = remaining[split_at:].lstrip()

    return chunks if chunks else ["(empty response)"]
