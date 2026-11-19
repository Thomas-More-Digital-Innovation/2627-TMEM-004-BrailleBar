"""OCR worker: reads files announced by document.uploaded and publishes document.ocr.completed.

It keeps no state, so scaling is only a matter of more replicas. One file at a time per replica, because OCR uses a
whole CPU core. Logs hold ids only, never the content.
"""

import asyncio
import json
import logging
import os
import signal
import urllib.request
import uuid
from datetime import datetime, timezone

import aio_pika

from ocr import Unreadable, read_document

EXCHANGE = "braillebar.events"
DEAD_EXCHANGE = "braillebar.dead"
QUEUE = "ocr-service"
UPLOADED = "document.uploaded"
COMPLETED = "document.ocr.completed"
STATUS = "print.job.status"
MAX_ATTEMPTS = 3
COULD_NOT_READ = "This file could not be read"

log = logging.getLogger("ocr-service")


def _envelope(source: dict, event_type: str, payload: dict) -> dict:
    # Derived from the source event, so a redelivered upload gives the same id and Layout's duplicate check skips it.
    event = {
        "id": str(uuid.uuid5(uuid.NAMESPACE_URL, f"{source['id']}/{event_type}")),
        "type": event_type,
        "occurredAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
        "payload": payload,
    }
    if source.get("traceparent"):
        event["traceparent"] = source["traceparent"]  # keeps this step in the same trace
    return event


def completed_event(source: dict, pages: list[dict]) -> dict:
    p = source["payload"]
    return _envelope(source, COMPLETED, {"documentId": p["documentId"], "deviceId": p["deviceId"], "pages": pages})


def failed_event(source: dict, reason: str) -> dict:
    """No print job exists yet, so the document id stands in for the job id. The app is told through the normal status."""
    p = source["payload"]
    return _envelope(source, STATUS, {
        "jobId": p["documentId"], "documentId": p["documentId"], "deviceId": p["deviceId"],
        "state": "failed", "page": 0, "pageCount": 0, "reason": reason,
    })


def fetch_and_read(document_id: str) -> list[dict]:
    url = f"{os.environ['DOCUMENT_SERVICE_URL']}/documents/{document_id}/file"
    with urllib.request.urlopen(url, timeout=30) as response:
        return read_document(response.read(), response.headers.get_content_type())


async def publish(exchange: aio_pika.abc.AbstractExchange, event: dict) -> None:
    message = aio_pika.Message(
        json.dumps(event).encode(), content_type="application/json", message_id=event["id"],
        delivery_mode=aio_pika.DeliveryMode.PERSISTENT,
    )
    await exchange.publish(message, routing_key=event["type"])  # returns once the broker confirmed it


async def handle(message: aio_pika.abc.AbstractIncomingMessage, exchange: aio_pika.abc.AbstractExchange) -> None:
    try:
        event = json.loads(message.body)
        document_id = event["payload"]["documentId"]
        needs_ocr = event["payload"].get("needsOcr", False)
    except (ValueError, KeyError, TypeError):
        log.error("unreadable event, dead-lettering")
        await message.nack(requeue=False)
        return
    if not needs_ocr:
        await message.ack()
        return

    for attempt in range(1, MAX_ATTEMPTS + 1):
        try:
            # in a thread, so the broker heartbeat keeps running during a long scan
            pages = await asyncio.to_thread(fetch_and_read, document_id)
        except Unreadable as e:
            log.info("file refused documentId=%s", document_id)
            await publish(exchange, failed_event(event, str(e)))
            await message.ack()
            return
        except Exception as e:  # network trouble or a damaged file
            log.warning("ocr failed documentId=%s attempt=%d error=%s", document_id, attempt, type(e).__name__)
            await asyncio.sleep(attempt * 0.5)
        else:
            await publish(exchange, completed_event(event, pages))
            await message.ack()
            log.info("document read documentId=%s pages=%d", document_id, len(pages))
            return

    log.error("event failed, dead-lettering documentId=%s", document_id)
    await publish(exchange, failed_event(event, COULD_NOT_READ))
    await message.nack(requeue=False)


async def connect(url: str) -> aio_pika.abc.AbstractConnection:
    while True:  # the worker can start before RabbitMQ is ready
        try:
            return await aio_pika.connect(url)
        except (OSError, aio_pika.exceptions.AMQPError) as e:
            log.warning("broker not ready: %s", type(e).__name__)
            await asyncio.sleep(2)


async def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")
    # Unacked work goes back to the queue, so stopping at any moment loses nothing.
    signal.signal(signal.SIGTERM, lambda *_: os._exit(0))

    connection = await connect(os.environ["RABBITMQ_URL"])
    # Same as the Go services: on a lost connection exit, and Docker restarts us with a fresh one.
    connection.close_callbacks.add(lambda *_: os._exit(1))
    channel = await connection.channel(publisher_confirms=True)
    await channel.set_qos(prefetch_count=1)

    exchange = await channel.declare_exchange(EXCHANGE, aio_pika.ExchangeType.TOPIC, durable=True)
    dead_exchange = await channel.declare_exchange(DEAD_EXCHANGE, aio_pika.ExchangeType.TOPIC, durable=True)
    dead = await channel.declare_queue(f"{QUEUE}.dead", durable=True)
    await dead.bind(dead_exchange, routing_key=QUEUE)
    queue = await channel.declare_queue(QUEUE, durable=True, arguments={
        "x-dead-letter-exchange": DEAD_EXCHANGE,
        "x-dead-letter-routing-key": QUEUE,
    })
    await queue.bind(exchange, routing_key=UPLOADED)

    log.info("listening queue=%s", QUEUE)
    async with queue.iterator() as messages:
        async for message in messages:
            await handle(message, exchange)


if __name__ == "__main__":
    asyncio.run(main())
