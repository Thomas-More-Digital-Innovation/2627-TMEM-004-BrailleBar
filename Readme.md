# WELCOME TO BRAILLEBAR
## This project will have a Webapp built in tauri and a mobile app in react native
BrailleBar is a braille printer that let's blind and visually impaired people print any text or document in braille, from their computer or phone.

The printer is built separately from this project, I mainly supply the software and code for the printer itself.

This software will allow the printer to print either simple typed in text or use OCR to scan a document and print it out in braille

## Deliveries
- A desktop App for Windows, macOS, Linux
- A mobile app for Android and IOS, with accessible camera scan
- A backend that handles documents, OCR, layout and print jobs
- A printer link over BLE

The printer does the braille translation itself, so the software only has to deliver clean text with the right layout. Keeping the original layout (positions and spacing) being one of my main requirements, because the structure of a letter, a form or a table changes.

## Users
- Blind or visually impaired person at home - Read mail, letters and documents in braille without help from someone else
- Student who is blind - Print course material, notes and exam sheets with the layout intact
- Support worker, teacher or family member - Send something to print for someone else, set up the printer
- Organisation (schools, libraries, associations for blind people) - Share a few printers between many users

## Main Use Cases
1. Type and print: the user types or dictates a sentence and prints it right away.
2. Print a file: the user picks a PDF, Word file or image and prints it.
3. Scan and print: the user photographs a paper letter with the phone, the app guides them by voice, and the result is printed
4. Share to print: the user shares a document or scan from another app (mail, Seeing AI, files) to the braille bar app.
5. Follow a print: the user hears the status ("printing page 2 of 3") through their screen reader.

## Scope
in scope:
- Desktop and mobile app
- Backend services: documents, OCR, layout, print jobs, notifications
- The bluetooth link from app to the ESP32, including the protocol and custom library as well as a refactor of the printer code.
- Tests, deployment and documentation

## Features
- Type or dictate text and print - Desktop, Mobile - Dictation uses the system speech input, no whisper or anything like that.
- Print PDF, Word or image files - Desktop (file picker and drag and drop), Mobile (file picker) - Sent to OCR when the file has no text layer
- Camera scan - Mobile - Voice guided
- Share to print from other apps - Desktop, Mobile
- Connect to printer over BLE - Desktop, Mobile - one device at a time
- Print Queue and live status - Desktop, Mobile - Announced by the screen reader

### Plan for accessible camera scan
The scan has to do the work for someone who cannot see the screen, so the app will do the aiming for the user.
1. The user places the paper on a table and holds the phone above it, or lays the phone flat on the page and lifts it slowly.
2. The app detects the page edges live and given spoken haptic hints like "move left" or "lift higher" with haptic feedback
3. It captures automatically once the full page is in frame and phone is steady
4. It checks the photo: turns on the flash when it's too dark, asks to hold still when it's blurry, and detects pages that are upside down.
5. It reads a short preview out loud, like the first line and the page count, and asks to confirm
6. For more pages it says "page 2 captured, turn the page or say done".
This will work on the phone for fast feedback, OCR itself will run on the feedback and be queued for documents with multiple pages.

## Expected architectuer
The system will be split into small backend serv ices taht talk to a message broker, plus a device agent inside each app that handles the printer. The backend runs in docker on a server (probably on railway), the Device agent runs on the pc or phone next to the printer.

Check Architecture.png

The apps only talk to the API gateway and get live updates from the Notification Service. All work after the upload moves as events thought RabbitMQ

### Flow of a document
1. The app uploads a file, photo or typed text thought the API gateway
2. Document Service stores it and publishes document.uploaded
3. An OCR worker extracts the text with positions and publishes document.ocr.completed. Typed text skips this step.
4. Layout Service fits the text to the printer's line width and publishes document.layout.completed
5. Print Job Service creates a job and publishes print.job.created
6. The Device Agent in the app sends the job to the print chunk by chunk and reports print.job.status
7. Notification Service pushes each status change to the app, which announces it through the screen reader (probably native one, don't want a collision between both mine and the native one)

### Why did I pick this design you say?
- OCR is very heavy, it will run as workers that can be scaled up without touching the rest of the stuff
- OCR and Layout are separate, so the OCR engine can be replaced without changing how text is formatted
- Async events keep the apps responsive while slow steps like OCR and printing run
- The device agent will live in the app as bluetooth only works with the device next to it
- New apps are cheap because the desktop and mobile apps use the same backend, not only can the services be scaled but the number of devices and printers too

## Components, tech stack
Client

- Desktop app - UI for typing, files, queueing and status - Tauri
- Mobile app - Same UI plus camera scan and share menu - React Native, Expo, Typescript
- Shared stuff - API client, event types, UI logic used by both apps (the backend is Go, so it does not use this) - Typescript
- Device agent - BLE connection, chunking, retries - Rust (Desktop), Typescript (Mobile)
- On-Device scan helper - Page edge detection, OCR - Undecided

Backend Services

- API gateway - Entry point for the apps, routing, rate limiting - Go
- Document Service - Uploads, file storage, metadata - Go, PostgreSQL
- OCR Service - Text plus word and line positions, scales with more workers - Python, FastAPI
- Layout Service - Fits OCR output into the printer's line width, keeps spacing - Go
- Print Job Service - Job states, retries, cancelling - Go, PostgreSQL
- Notification Service - Pushes status to the apps - Go, Websockets

The backend services are written in Go. The OCR Service stays in Python because the OCR engines live there.

Because the backend is Go and the apps are TypeScript, the event types cannot be shared as code. The events are described once in a language neutral schema, and both sides are tested against it.

Infra

- Message Broker - RabbitMQ
- Database - PostgreSQL, one schema per service
- Containers - Docker, Docker Compose
- CI/CD - Github Actions
- Logs and tracing - Structured JSON logs, OpenTelemetry
- Testing - Go tests, pytest, Testcontainers, Playwright, k6

## Data Model
Each service owns it's own data tables. They refer to each other only by ID, never with a foreign key across services
Table - Owned by
- Device - API Gatewway
- Printer - Print Job Service
- Document - Document Service
- OCRresult - OCR Service
- PrintText - Layout Service
- PrintJob - Print Job Service
- ProcessedEvent - Every consuming service

### Print job states
- queued - sending - printing - done

## Printer communication
The apps talk to the printer over BLE, mainly for IOS support, otherwise we would use classic.

Device agent -> BLE -> ESP32 -> printing mechanism

### BLE service on the ESP32
- RX (write) - from app to printer, used for chunks of text and commands
- TX (notify) - from printer to app, ACK, NAK and status messages

### Messages

- Chunk - from app - CHUNK|<jobId>|<seq>|<text>|<checksum>
- Acknowledge - from printer - ACK|<seq>
- Reject - from printer - NAK|<seq>
- Status request - from app - STATUS
- Status - from printer - STATE|<ready, busy, paper_out, error>
- Pause/cancel - from app - PAUSE, CANCEL|<jobId>

- The agent sends one chunk and will wait for an ACK before sending the next
- No answer within the timeout, or a NAK, means the chunk is sent again, up to a set number of tries
- The printer remembers the last sequence number it printed and ignroes a chunk it already has, so a resend never prints twice
- after a lost connection the agent reconnects with backoff and asks for STATUS before continuing
- only one app can be connected at a time. A second device will get a busy status

## Non-functional requirements
### Accessibility (highest prio)
- Every screen works fully with VoiceOver, TalkBackand NVDA, and on desktop with the desktop as navigation
- Every status change is announced, not only shown
- Clear labels on every button, logical focus order, no actions that need precise tapping or dragging
- Large text and high contrast for users with low vision
- Permission prompts (Bluetooth, camera) are explained in plain words before the system asks
- Tested with real blind user before first release

### Reliability
- A print never stops halfway without telling the user why
- After a crash, lost connection or locked phone, a job continues from the last confirmed chunk
- No line is ever printed twice
- If one backend service is down, uploads still work and wait in queue

### Privacy and Security
- Documents can be personal so they are encrypted in storage and over the network HTTPS
- Documents are deleted automatically once the print is done or after a short set time
- No login, each device only sees its own documents and print jobs through its anonymous device ID
- No document content in logs, only IDs
- No accounts and no personal data stored besides the documents themselves

### Performance
- Typed text starts printing within a few seconds
- The camera scan gives feedback in real time, without delay
- OCR scales by adding workers, so a busy moments doesn't block everyone

### Maintainability
- Each service can be changed and deployed on it's own
- The OCR engine can be swapped without touching the other services
- The BLE protocol is written down with test cases both agents must pass

## Deployment
Local - Docker compose on my laptop
Test - Railway for backend
Prod - VPS, Kubernetes when there are many more printers

### Backend
- Every service is it's own Docker image, built by Railway on each push
- A reverse proxy, Nginx in front of the API gateway for HTTPS
- Health check endpoint on each service so crashed containers restart automatically
- Daily database backup

### Apps
- Desktop: Tauri installers for Windows, MacOS, Linux
- Mobile: EXPO EAS builds for Android and IOS
- The apps get the backend URL from config