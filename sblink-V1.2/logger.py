import os
import sys
import logging
from logging.handlers import RotatingFileHandler
from pathlib import Path


class CustomFormatter(logging.Formatter):
    def format(self, record):
        if hasattr(record, "custom_tag"):
            message = record.getMessage()
            asctime = self.formatTime(record, self.datefmt)
            return f"[{asctime}] [{record.custom_tag}] {message}"
        return super().format(record)


log_path = Path(__file__).parent / "sblink.log"

logger = logging.getLogger("sblink")
logger.setLevel(logging.INFO)

formatter = CustomFormatter("[%(asctime)s] [%(levelname)s] %(message)s", datefmt="%Y-%m-%d %H:%M:%S")

if sys.stdout is not None:
    ch = logging.StreamHandler(sys.stdout)
    ch.setFormatter(formatter)
    logger.addHandler(ch)

fh = RotatingFileHandler(log_path, maxBytes=1024 * 1024, backupCount=2, encoding="utf-8")
fh.setFormatter(formatter)
logger.addHandler(fh)


def log(tag, message):
    logger.info(message, extra={"custom_tag": tag})


def enable_console_output():
    """Wire up a console stream handler after stdout becomes available (e.g. after AllocConsole)"""
    if sys.stdout is None:
        return
    for h in logger.handlers:
        if isinstance(h, logging.StreamHandler) and getattr(h, "stream", None) is sys.stdout:
            return
    ch = logging.StreamHandler(sys.stdout)
    ch.setFormatter(formatter)
    logger.addHandler(ch)
