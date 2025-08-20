# mint_proxy API Usage Examples

## Overview

This document provides practical examples of how to interact with the mint_proxy WebSocket API for requesting Lightning invoices and receiving Cashu tokens.

## WebSocket Connection

Connect to the mint_proxy WebSocket server:

```javascript
const ws = new WebSocket('ws://localhost:2122/mint-proxy');

ws.onopen = function(event) {
    console.log('Connected to mint_proxy');
};

ws.onmessage = function(event) {
    const message = JSON.parse(event.data);
    handleMessage(message);
};

ws.onerror = function(error) {
    console.error('WebSocket error:', error);
};
```

## Example 1: Basic Mint Request Flow

### Step 1: Request Lightning Invoice

Send a mint request to generate a Lightning invoice:

```javascript
const mintRequest = {
    type: "mint_request",
    mint_url: "https://mint.minibits.cash/Bitcoin",
    amount: 1000  // Amount in satoshis
};

ws.send(JSON.stringify(mintRequest));
```

### Step 2: Receive Invoice

The mint_proxy will respond with a Lightning invoice:

```javascript
function handleMessage(message) {
    switch(message.type) {
        case 'invoice_ready':
            console.log('Invoice received:', message.invoice);
            console.log('Request ID:', message.request_id);
            console.log('Expires at:', new Date(message.expires_at * 1000));
            
            // Display QR code or copy invoice to clipboard
            displayInvoice(message.invoice);
            break;
            
        case 'tokens_ready':
            console.log('Tokens received!', message.tokens);
            console.log('Request ID:', message.request_id);
            
            // Store or process the Cashu tokens
            processTokens(message.tokens);
            break;
            
        case 'error':
            console.error('Error:', message.code, message.message);
            break;
    }
}
```

## Example 2: Complete Implementation

```javascript
class MintProxyClient {
    constructor(wsUrl = 'ws://localhost:2122/mint-proxy') {
        this.ws = new WebSocket(wsUrl);
        this.setupEventHandlers();
        this.pendingRequests = new Map();
    }
    
    setupEventHandlers() {
        this.ws.onopen = () => {
            console.log('Connected to mint_proxy');
        };
        
        this.ws.onmessage = (event) => {
            const message = JSON.parse(event.data);
            this.handleMessage(message);
        };
        
        this.ws.onerror = (error) => {
            console.error('WebSocket error:', error);
        };
        
        this.ws.onclose = () => {
            console.log('Disconnected from mint_proxy');
            // Implement reconnection logic if needed
        };
    }
    
    requestInvoice(mintUrl, amount) {
        const request = {
            type: "mint_request",
            mint_url: mintUrl,
            amount: amount
        };
        
        this.ws.send(JSON.stringify(request));
    }
    
    handleMessage(message) {
        switch(message.type) {
            case 'invoice_ready':
                this.onInvoiceReady(message);
                break;
                
            case 'tokens_ready':
                this.onTokensReady(message);
                break;
                
            case 'error':
                this.onError(message);
                break;
                
            default:
                console.warn('Unknown message type:', message.type);
        }
    }
    
    onInvoiceReady(message) {
        console.log('Invoice ready:', message);
        
        // Store request for tracking
        this.pendingRequests.set(message.request_id, {
            invoice: message.invoice,
            expires_at: message.expires_at,
            status: 'invoice_ready'
        });
        
        // Trigger UI update
        this.displayInvoice(message.invoice, message.request_id);
    }
    
    onTokensReady(message) {
        console.log('Tokens ready:', message);
        
        // Update request status
        if (this.pendingRequests.has(message.request_id)) {
            this.pendingRequests.get(message.request_id).status = 'tokens_ready';
            this.pendingRequests.get(message.request_id).tokens = message.tokens;
        }
        
        // Process the received tokens
        this.processTokens(message.tokens, message.request_id);
    }
    
    onError(message) {
        console.error('mint_proxy error:', message);
        
        // Handle different error types
        switch(message.code) {
            case 'invalid_mint':
                this.showError('The selected mint is not accepted by this tollgate');
                break;
                
            case 'mint_unavailable':
                this.showError('The mint is currently unavailable. Please try again later');
                break;
                
            case 'invoice_expired':
                this.showError('The invoice has expired. Please request a new one');
                break;
                
            case 'payment_failed':
                this.showError('Payment could not be completed');
                break;
                
            case 'token_claim_failed':
                this.showError('Failed to claim tokens from mint');
                break;
                
            default:
                this.showError(`Error: ${message.message}`);
        }
    }
    
    displayInvoice(invoice, requestId) {
        // Implementation for displaying invoice QR code
        console.log('Display invoice for request:', requestId);
        
        // Example: Create QR code
        // const qr = qrcode.toDataURL(invoice);
        // document.getElementById('qr-code').src = qr;
        
        // Example: Copy to clipboard
        navigator.clipboard.writeText(invoice).then(() => {
            console.log('Invoice copied to clipboard');
        });
    }
    
    processTokens(tokens, requestId) {
        // Implementation for processing received tokens
        console.log('Processing tokens for request:', requestId);
        
        // Example: Store tokens locally
        localStorage.setItem(`cashu_tokens_${requestId}`, tokens);
        
        // Example: Add to wallet
        // wallet.addTokens(tokens);
        
        // Cleanup pending request
        this.pendingRequests.delete(requestId);
    }
    
    showError(message) {
        // Implementation for displaying errors to user
        console.error(message);
        // document.getElementById('error-message').textContent = message;
    }
}

// Usage
const client = new MintProxyClient();

// Request invoice for 1000 sats from minibits
client.requestInvoice('https://mint.minibits.cash/Bitcoin', 1000);
```

## Example 3: React Integration

```jsx
import React, { useState, useEffect, useRef } from 'react';
import QRCode from 'qrcode.react';

function MintProxyClient() {
    const [ws, setWs] = useState(null);
    const [invoice, setInvoice] = useState('');
    const [tokens, setTokens] = useState('');
    const [status, setStatus] = useState('disconnected');
    const [error, setError] = useState('');
    
    useEffect(() => {
        const websocket = new WebSocket('ws://localhost:2122/mint-proxy');
        
        websocket.onopen = () => {
            setStatus('connected');
            setError('');
        };
        
        websocket.onmessage = (event) => {
            const message = JSON.parse(event.data);
            handleMessage(message);
        };
        
        websocket.onerror = () => {
            setStatus('error');
            setError('Connection failed');
        };
        
        websocket.onclose = () => {
            setStatus('disconnected');
        };
        
        setWs(websocket);
        
        return () => {
            websocket.close();
        };
    }, []);
    
    const handleMessage = (message) => {
        switch(message.type) {
            case 'invoice_ready':
                setInvoice(message.invoice);
                setStatus('invoice_ready');
                break;
                
            case 'tokens_ready':
                setTokens(message.tokens);
                setStatus('tokens_ready');
                setInvoice(''); // Clear invoice
                break;
                
            case 'error':
                setError(`${message.code}: ${message.message}`);
                setStatus('error');
                break;
        }
    };
    
    const requestInvoice = () => {
        if (ws && ws.readyState === WebSocket.OPEN) {
            const request = {
                type: "mint_request",
                mint_url: "https://mint.minibits.cash/Bitcoin",
                amount: 1000
            };
            
            ws.send(JSON.stringify(request));
            setStatus('requesting');
            setError('');
            setInvoice('');
            setTokens('');
        }
    };
    
    return (
        <div className="mint-proxy-client">
            <h2>Mint Proxy Client</h2>
            
            <div className="status">
                Status: {status}
                {error && <div className="error">Error: {error}</div>}
            </div>
            
            <button 
                onClick={requestInvoice} 
                disabled={status !== 'connected' && status !== 'tokens_ready'}
            >
                Request 1000 sats
            </button>
            
            {invoice && (
                <div className="invoice">
                    <h3>Pay this Lightning Invoice:</h3>
                    <QRCode value={invoice} size={256} />
                    <div className="invoice-text">
                        <small>{invoice}</small>
                    </div>
                    <button onClick={() => navigator.clipboard.writeText(invoice)}>
                        Copy Invoice
                    </button>
                </div>
            )}
            
            {tokens && (
                <div className="tokens">
                    <h3>Tokens Received!</h3>
                    <textarea 
                        value={tokens} 
                        readOnly 
                        rows="4" 
                        cols="50"
                    />
                    <button onClick={() => navigator.clipboard.writeText(tokens)}>
                        Copy Tokens
                    </button>
                </div>
            )}
        </div>
    );
}

export default MintProxyClient;
```

## Error Handling Best Practices

### 1. Connection Management

```javascript
function createReconnectingWebSocket(url, maxRetries = 5) {
    let retries = 0;
    
    function connect() {
        const ws = new WebSocket(url);
        
        ws.onopen = () => {
            retries = 0;
            console.log('Connected to mint_proxy');
        };
        
        ws.onclose = () => {
            if (retries < maxRetries) {
                retries++;
                console.log(`Reconnecting... (${retries}/${maxRetries})`);
                setTimeout(connect, 1000 * retries);
            } else {
                console.error('Max reconnection attempts reached');
            }
        };
        
        return ws;
    }
    
    return connect();
}
```

### 2. Request Timeout Handling

```javascript
class MintProxyClient {
    constructor() {
        this.requestTimeouts = new Map();
    }
    
    requestInvoice(mintUrl, amount, timeoutMs = 30000) {
        const request = {
            type: "mint_request",
            mint_url: mintUrl,
            amount: amount
        };
        
        this.ws.send(JSON.stringify(request));
        
        // Set timeout for this request
        const timeoutId = setTimeout(() => {
            this.onError({
                code: 'request_timeout',
                message: 'Request timed out'
            });
        }, timeoutMs);
        
        // Store timeout ID (you'd need to track request IDs properly)
        this.requestTimeouts.set('current', timeoutId);
    }
    
    onInvoiceReady(message) {
        // Clear timeout
        if (this.requestTimeouts.has('current')) {
            clearTimeout(this.requestTimeouts.get('current'));
            this.requestTimeouts.delete('current');
        }
        
        // Handle invoice...
    }
}
```

## Integration with Existing Tollgate Flow

If you want to integrate the mint_proxy with the existing tollgate payment system:

```javascript
// After receiving tokens, automatically add to session
async function addTokensToSession(tokens) {
    try {
        const response = await fetch('/fund-session', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
            },
            body: JSON.stringify({
                cashuToken: tokens
            })
        });
        
        if (response.ok) {
            console.log('Tokens added to session successfully');
        } else {
            console.error('Failed to add tokens to session');
        }
    } catch (error) {
        console.error('Error adding tokens to session:', error);
    }
}
```

This documentation provides comprehensive examples for integrating with the mint_proxy WebSocket API, from basic usage to advanced React implementations with proper error handling and reconnection logic.