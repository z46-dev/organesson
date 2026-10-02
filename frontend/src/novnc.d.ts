declare module "@novnc/novnc" {
    export default class RFB {
        constructor(target: HTMLElement, urlOrChannel: string | WebSocket, options?: { credentials?: { password?: string } });
        scaleViewport: boolean;
        resizeSession: boolean;
        focusOnClick: boolean;
        disconnect(): void;
        sendCredentials(credentials: { password?: string }): void;
        addEventListener(type: string, listener: (event: Event) => void): void;
    }
}
