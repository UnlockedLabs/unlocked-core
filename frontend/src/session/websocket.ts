import { WsMsg, WsEventType, OcActivityUpdate, User } from '@/types';

export class WebsocketSession {
    private socket: WebSocket | null = null;
    private currentActivityId = 0;
    private readonly session_id: string;
    private readonly userId: number;
    private readonly reconnectInterval: number = 5000;
    private currentMsgHandler: (msg: Partial<WsMsg>) => void = () => {
        return;
    };

    constructor(user: User) {
        this.userId = user.id;
        this.session_id = user.session_id;
        this.addWindowListeners();
    }

    private addWindowListeners(): void {
        window.addEventListener('focus', this.handleFocusChange);
        window.addEventListener(
            'visibilitychange',
            this.handleVisibilityChange
        );
        window.addEventListener('logoutEvent', this.handleLogout);
    }

    private removeWindowListeners(): void {
        window.removeEventListener('focus', this.handleFocusChange);
        window.removeEventListener(
            'visibilitychange',
            this.handleVisibilityChange
        );
        window.removeEventListener('logoutEvent', this.handleLogout);
    }

    private handleFocusChange = (): void => {
        if (this.socket && document.hidden) {
            this.tearDownConnection(false);
        } else if (!this.socket) {
            this.createConnection();
        }
    };

    private handleVisibilityChange = (): void => {
        if (!document.hidden && !this.socket) {
            this.createConnection();
        } else if (document.hidden && this.socket) {
            this.tearDownConnection(false);
        }
    };

    private handleLogout = (): void => {
        this.tearDownConnection(true);
    };

    public connect(): void {
        this.createConnection();
    }

    private createConnection(): void {
        if (this.socket) return;
        const webSocketUrl = `/api/ws/listen`;
        this.socket = new WebSocket(webSocketUrl);

        this.socket.onopen = () => {
            const message: WsMsg = {
                event_type: WsEventType.ClientHello,
                msg: {
                    msg: 'client_hello',
                    activity_id: 0
                },
                user_id: this.userId,
                session_id: this.session_id
            };
            this.sendMessage(message);
        };

        this.socket.onmessage = (event: MessageEvent<string>) => {
            const msg = this.defaultMsgHandler(event);
            this.currentMsgHandler(msg);
        };

        this.socket.onclose = () => {
            this.socket = null;
            setTimeout(() => {
                if (!document.hidden) {
                    this.createConnection();
                }
            }, this.reconnectInterval);
        };

        this.socket.onerror = () => {
            // connection errors handled by onclose reconnect
        };
    }

    public notifyOpenContentActivity(cleanup?: boolean): void {
        if (this.socket?.readyState === WebSocket.OPEN) {
            const msg: WsMsg = {
                event_type: WsEventType.VisitEvent,
                msg: {
                    activity_id: this.currentActivityId,
                    msg: ''
                },
                user_id: this.userId,
                session_id: this.session_id
            };
            this.socket.send(JSON.stringify(msg));
        } else {
            this.connect();
        }
        if (cleanup) {
            this.currentActivityId = 0;
        }
    }

    public sendMessage(msg: WsMsg): void {
        if (this.socket?.readyState === WebSocket.OPEN) {
            this.socket.send(JSON.stringify(msg));
        }
    }

    public tearDownConnection(removeListeners: boolean): void {
        if (this.socket) {
            const sessionMessage: WsMsg = {
                event_type: WsEventType.ClientGoodbye,
                user_id: this.userId,
                session_id: this.session_id,
                msg: { activity_id: this.currentActivityId, msg: '' }
            };
            this.sendMessage(sessionMessage);
            this.socket.close();
            this.socket = null;
        }
        if (removeListeners) {
            this.removeWindowListeners();
        }
    }

    public setMsgHandler(handler: (msg: Partial<WsMsg>) => void): void {
        this.currentMsgHandler = handler;
    }

    public resetMsgHandler(): void {
        this.currentMsgHandler = () => {
            return;
        };
    }

    private defaultMsgHandler(event: MessageEvent<string>): Partial<WsMsg> {
        try {
            const data = JSON.parse(event.data) as Partial<WsMsg>;
            if (data.event_type !== undefined) {
                if (data.event_type === WsEventType.VisitEvent) {
                    if (this.currentActivityId !== 0) {
                        this.notifyOpenContentActivity();
                    }
                    this.currentActivityId = (
                        data.msg as OcActivityUpdate
                    ).activity_id;
                }
            }
            return data;
        } catch {
            // malformed message, ignore
        }
        return {};
    }
}

export default WebsocketSession;
