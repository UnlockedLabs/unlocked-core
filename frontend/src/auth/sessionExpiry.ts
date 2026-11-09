// Lets the API layer report a 401 to AuthProvider without importing it, so a
// session that ends mid-use sends the resident to login instead of leaving
// every click silently failing.
type Handler = () => void;

let handler: Handler | undefined;

export function onSessionExpired(h: Handler): () => void {
    handler = h;
    return () => {
        if (handler === h) handler = undefined;
    };
}

export function reportUnauthorized(): void {
    handler?.();
}
