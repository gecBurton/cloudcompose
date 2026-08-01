# WebSockets on GCP: How They Actually Work

## The Short Answer

**Yes, WebSockets work on GCP Cloud Run, but with important caveats.**

---

## How WebSockets Work on Cloud Run

### 1. **WebSockets ARE Supported**

Cloud Run supports WebSockets with no additional configuration. They're treated as long-running HTTP requests.

### 2. **The Request Timeout Problem**

**Critical limitation**: WebSocket connections are subject to the **request timeout** (default 5 minutes, max 60 minutes).

```yaml
# You MUST increase timeout for WebSockets
gcloud run services update my-service --timeout=3600  # 60 minutes
```

After timeout: Connection is forcibly closed. Clients must **reconnect**.

### 3. **The Multi-Instance Problem**

**The real challenge**: Cloud Run scales horizontally. Multiple instances = no shared state.

**Scenario**:
```
Client A → Instance 1  # Joins chat room "general"
Client B → Instance 2  # Joins chat room "general"
```

**Problem**: They can't see each other's messages because instances don't share memory.

### 4. **The Solution: External Message Broker**

**Required architecture**:

```
Client A → Instance 1 → Redis Pub/Sub ← Instance 2 ← Client B
                ↓                           ↓
           Subscribe to              Subscribe to
           "general" channel          "general" channel
```

**Popular options**:
- **Redis Pub/Sub** (Memorystore) - Most common
- **Firestore real-time updates** - Google-native
- **Pub/Sub** - For async, not real-time sync

### 5. **Session Affinity (Sticky Sessions)**

Cloud Run offers **best-effort** session affinity:

```yaml
gcloud run services update my-service --session-affinity
```

**Not guaranteed**: Reconnects might still go to different instances.

### 6. **Concurrency Matters**

**Default**: 80 concurrent requests per instance  
**WebSocket apps**: Should increase to 1000

```yaml
gcloud run services update my-service --concurrency=1000
```

### 7. **Billing Implications**

**Important**: Any instance with an open WebSocket is **always billed** (CPU always allocated).

- No scale-to-zero while connections are open
- Can get expensive for many persistent connections

---

## Real-World Architecture: Chat App on Cloud Run

```yaml
services:
  # WebSocket server
  chat:
    image: myapp/chat
    x-composey:
      type: http_service
      timeout: 3600          # 60 minutes
      concurrency: 1000      # Max connections per instance
      session_affinity: true # Best-effort sticky
    connections:
      - target: redis
        type: cache
        purpose: pubsub      # For cross-instance sync

  # Message broker
  redis:
    image: redis:7
    x-composey:
      capability: cache
      size: medium           # Needs to handle pub/sub load
```

**Flow**:
1. Client connects to chat service (WebSocket)
2. Instance subscribes to Redis channel
3. Client sends message → Instance publishes to Redis
4. All instances receive → Forward to their connected clients

---

## Comparison: WebSockets Across Clouds

| Aspect | AWS | Azure | GCP |
|--------|-----|-------|-----|
| **Native support** | ✅ ALB | ✅ Built-in | ✅ Cloud Run |
| **Timeout** | 60 min (ALB) | No limit | 60 max |
| **Multi-instance state** | Same problem | Same problem | Same problem |
| **Solution** | Redis/ElastiCache | Redis | Memorystore |
| **Session affinity** | ALB stickiness | Built-in | Best-effort |
| **Scale-to-zero** | ❌ While connected | ❌ While connected | ❌ While connected |

**Key insight**: All three clouds have the same fundamental problem - **stateless instances can't share WebSocket state**.

The solution (external message broker) is the same everywhere.

---

## Implications for Our Design

### 1. **WebSockets ARE Possible on Serverless**

But they require:
- Increased timeouts
- External message broker (Redis)
- Client reconnection logic
- No true scale-to-zero (while connections open)

### 2. **The Abstraction Should Support This**

```yaml
services:
  websocket:
    x-composey:
      type: http_service
      websocket: true        # Enable WebSocket mode
      timeout: 3600
      concurrency: 1000
    connections:
      - target: redis
        purpose: pubsub      # Required for multi-instance
```

### 3. **The "Serverless" Trade-off**

| Without WebSockets | With WebSockets |
|-------------------|-----------------|
| ✅ Scale-to-zero | ❌ Always billed while connected |
| ✅ Simple | ❌ Complex (Redis required) |
| ✅ Cheap | ❌ More expensive |
| ✅ Stateless | ❌ Requires state sync |

### 4. **Recommendation for Our Design**

**Support WebSockets as a specific service type**:

```yaml
services:
  # Pure serverless - scale to zero
  api:
    x-composey:
      type: http_service
      
  # WebSocket - requires broker
  chat:
    x-composey:
      type: websocket_service  # New type
      broker: redis            # Required
      timeout: 3600
```

**Implementation per cloud**:
- **AWS**: ALB + ECS/EKS + ElastiCache
- **Azure**: Container Apps + built-in + Azure Cache
- **GCP**: Cloud Run + Memorystore

---

## Conclusion

**WebSockets on GCP are real and production-ready**, but they require:

1. Understanding the 60-minute timeout limit
2. Accepting no scale-to-zero while connected
3. Adding a message broker (Redis) for multi-instance sync
4. Client reconnection logic

**This isn't a GCP limitation** - it's a fundamental characteristic of stateless, auto-scaling serverless platforms. All clouds have the same issue.

**Our abstraction should**: Support WebSockets explicitly, make the trade-offs clear, and provide the Redis connection automatically.
