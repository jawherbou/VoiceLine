# VoiceLine

A Go backend that takes a voice memo, transcribes it, extracts a structured note using AI, and saves it to a Google Sheet.

> This readme will have mainly two parts. First part will answer the question of how to build
voiceLine if it was a full mobile app where users record voice memos that are automatically transcribed, summarized, and routed into productivity tools. The second part will answer how to setup this minimal go repo.

## VoiceLine The app

### System Architecture
**Mobile App (React Native)**
- Records audio
- Uploads to /v1/audio/process as multipart form data
- Displays the returned structured note and integration status

↓ HTTPS multipart/form-data

**Go / Gin API**
- Validates incoming audio and checks auth (JWT middleware)
- Stores raw audio in S3
- Enqueues a processing job onto Redis Stream (or Rabbitmq/any queuing system)
- Returns 202 Accepted + job_id immediately so the client isn't left waiting

**Worker goroutine**
- Reads jobs from Redis Stream
- Calls Whisper → gets transcript
- Calls GPT-4o → extracts structured note (title, summary, actions, tags)
- Persists the note to PostgreSQL
- Runs integration adapters to push the note downstream
- Notifies the mobile app via WebSocket or push notification

↓ splits into two downstream targets

**AI (Groq / OpenAI)**
- Whisper for transcription
- LLM for structured note extraction

**Integration Adapters**
- Google Sheets (this demo)
- Notion API

## VoiceLine the demo

### What it does

```
POST /v1/audio/process  (multipart, field: "audio")
        │
        ├─→ OpenAI Whisper API  →  raw transcript
        │
        ├─→ Groq  →  { title, summary, action_items[], tags[] }
        │
        └─→ Google Sheets API  →  append row
                │
                └─→ 200 JSON { note, sheet_url }
```

---

#### Prerequisites

- Go 1.22+
- An OpenAI API key
- A Google Sheet + OAuth token — see **Google Sheets setup** below

#### 1. Clone & configure

```bash
git clone https://github.com/jawherbou/voiceline-backend
cd voiceline-backend

cp .env.example .env
# Fill in OPENAI_API_KEY, GOOGLE_SHEET_ID, GOOGLE_SHEETS_TOKEN
```

#### 2. Run

```bash
go mod download
go run ./cmd/server
# → listening on :8080
```

#### 3. Test with a real audio file

```bash
curl -X POST http://localhost:8080/v1/audio/process \
     -F "audio=@/path/to/memo.m4a"
```

Example response:

```json
{
  "note": {
    "title": "",
    "summary": "No meaningful discussion",
    "action_items": [],
    "tags": [],
    "raw_transcript": "What's going to do? We'll come back here. Here we go. We'll be thatstar. We'll be thatstar. Bye bye.",
    "created_at": "2026-03-06T00:48:12+01:00"
  },
  "resource_url": "https://docs.google.com/spreadsheets/d/1SOZkNdhY4qVlhCSB-5EMJEJepw-rdD2IFy5ZtwxJ4KI"
}
```

### Google Sheets Setup

1. Create a new Google Sheet.
2. Name the first tab **Notes**.
3. Add headers in row 1 (you can skip this also):  
   `CreatedAt | Title | Summary | ActionItems | Tags | RawTranscript`
4. Copy the Sheet ID from the URL:  
   `https://docs.google.com/spreadsheets/d/**<SHEET_ID>**/edit`
5. **Auth – choose one:**
   - Go to the OAuth Playground (developers.google.com/oauthplayground)
   - Select the Sheets scope
   - Exchange for an access token

