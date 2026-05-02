"""Main entry point for Claude Code WhatsApp Bot."""

import argparse
import asyncio
import logging
import signal
import sys
from pathlib import Path
from typing import Any, Dict, Optional

import structlog

from src import __version__
from src.claude import ClaudeIntegration, SessionManager
from src.claude.sdk_integration import ClaudeSDKManager
from src.config.features import FeatureFlags
from src.config.settings import Settings
from src.events.bus import EventBus
from src.events.handlers import AgentHandler
from src.events.middleware import EventSecurityMiddleware
from src.exceptions import ConfigurationError
from src.security.audit import AuditLogger, InMemoryAuditStorage
from src.security.auth import (
    AuthenticationManager,
    InMemoryTokenStorage,
    TokenAuthProvider,
    WhitelistAuthProvider,
)
from src.security.rate_limiter import RateLimiter
from src.security.validators import SecurityValidator
from src.storage.facade import Storage
from src.storage.session_storage import SQLiteSessionStorage
from src.whatsapp.core import WhatsAppBot


def setup_logging(debug: bool = False) -> None:
    """Configure structured logging."""
    level = logging.DEBUG if debug else logging.INFO
    logging.basicConfig(level=level, format="%(message)s", stream=sys.stdout)

    structlog.configure(
        processors=[
            structlog.stdlib.filter_by_level,
            structlog.stdlib.add_logger_name,
            structlog.stdlib.add_log_level,
            structlog.stdlib.PositionalArgumentsFormatter(),
            structlog.processors.TimeStamper(fmt="iso"),
            structlog.processors.StackInfoRenderer(),
            structlog.processors.format_exc_info,
            structlog.processors.UnicodeDecoder(),
            (
                structlog.processors.JSONRenderer()
                if not debug
                else structlog.dev.ConsoleRenderer()
            ),
        ],
        context_class=dict,
        logger_factory=structlog.stdlib.LoggerFactory(),
        wrapper_class=structlog.stdlib.BoundLogger,
        cache_logger_on_first_use=True,
    )


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Claude Code WhatsApp Bot")
    parser.add_argument("--version", action="version", version=f"v{__version__}")
    parser.add_argument("--debug", action="store_true", help="Enable debug logging")
    parser.add_argument("--config-file", type=Path, help="Path to configuration file")
    return parser.parse_args()


async def create_application(config: Settings) -> Dict[str, Any]:
    """Create and configure application components."""
    logger = structlog.get_logger()
    logger.info("Creating application components")

    # Storage
    storage = Storage(config.database_url)
    await storage.initialize()

    # Auth — use phone-based whitelist
    providers = []
    if config.allowed_users:
        providers.append(WhitelistAuthProvider(config.allowed_users))
    if config.enable_token_auth:
        token_storage = InMemoryTokenStorage()
        providers.append(TokenAuthProvider(config.auth_token_secret, token_storage))
    if not providers and config.development_mode:
        providers.append(WhitelistAuthProvider([], allow_all_dev=True))
    elif not providers:
        raise ConfigurationError("No authentication providers configured")

    auth_manager = AuthenticationManager(providers)
    security_validator = SecurityValidator(
        config.approved_directory,
        disable_security_patterns=config.disable_security_patterns,
    )
    rate_limiter = RateLimiter(config)
    audit_storage = InMemoryAuditStorage()
    audit_logger = AuditLogger(audit_storage)

    # Claude integration
    session_storage = SQLiteSessionStorage(storage.db_manager)
    session_manager = SessionManager(config, session_storage)
    sdk_manager = ClaudeSDKManager(config, security_validator=security_validator)
    claude_integration = ClaudeIntegration(
        config=config,
        sdk_manager=sdk_manager,
        session_manager=session_manager,
    )

    # Event bus
    event_bus = EventBus()
    event_security = EventSecurityMiddleware(
        event_bus=event_bus,
        security_validator=security_validator,
        auth_manager=auth_manager,
    )
    event_security.register()
    agent_handler = AgentHandler(
        event_bus=event_bus,
        claude_integration=claude_integration,
        default_working_directory=config.approved_directory,
        default_user_id=config.allowed_users[0] if config.allowed_users else 0,
    )
    agent_handler.register()

    dependencies = {
        "auth_manager": auth_manager,
        "security_validator": security_validator,
        "rate_limiter": rate_limiter,
        "audit_logger": audit_logger,
        "claude_integration": claude_integration,
        "storage": storage,
        "event_bus": event_bus,
    }

    bot = WhatsAppBot(config, dependencies)

    return {
        "bot": bot,
        "claude_integration": claude_integration,
        "storage": storage,
        "config": config,
        "event_bus": event_bus,
    }


async def run_application(app: Dict[str, Any]) -> None:
    """Run application with graceful shutdown."""
    logger = structlog.get_logger()
    bot: WhatsAppBot = app["bot"]
    claude_integration: ClaudeIntegration = app["claude_integration"]
    storage: Storage = app["storage"]
    event_bus: EventBus = app["event_bus"]

    shutdown_event = asyncio.Event()

    def signal_handler(signum: int, frame: Any) -> None:
        logger.info("Shutdown signal received", signal=signum)
        shutdown_event.set()

    signal.signal(signal.SIGINT, signal_handler)
    signal.signal(signal.SIGTERM, signal_handler)

    try:
        logger.info("Starting Claude Code WhatsApp Bot")
        await event_bus.start()

        bot_task = asyncio.create_task(bot.start())
        shutdown_task = asyncio.create_task(shutdown_event.wait())

        done, pending = await asyncio.wait(
            [bot_task, shutdown_task], return_when=asyncio.FIRST_COMPLETED
        )

        for task in done:
            if task.cancelled():
                continue
            exc = task.exception()
            if exc:
                logger.error("Task failed", error=str(exc))

        for task in pending:
            task.cancel()
            try:
                await task
            except asyncio.CancelledError:
                pass

    finally:
        logger.info("Shutting down")
        await event_bus.stop()
        await bot.stop()
        await claude_integration.shutdown()
        await storage.close()
        logger.info("Shutdown complete")


async def main() -> None:
    args = parse_args()
    setup_logging(debug=args.debug)
    logger = structlog.get_logger()
    logger.info("Starting Claude Code WhatsApp Bot", version=__version__)

    try:
        from src.config import load_config
        config = load_config(config_file=args.config_file)
        app = await create_application(config)
        await run_application(app)
    except ConfigurationError as e:
        logger.error("Configuration error", error=str(e))
        sys.exit(1)
    except Exception as e:
        logger.exception("Unexpected error", error=str(e))
        sys.exit(1)


def run() -> None:
    try:
        asyncio.run(main())
    except KeyboardInterrupt:
        print("\nShutdown requested")
        sys.exit(0)


if __name__ == "__main__":
    run()
