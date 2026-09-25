/**
 * This file is the entry point for the React app, it sets up the root
 * element and renders the App component to the DOM.
 *
 * It is included in `src/index.html`.
 */

import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";

const element = document.getElementById("root");

if (element === null) {
    throw new Error("The application root is missing.");
}

const app = (
    <StrictMode>
        <App />
    </StrictMode>
);

if (import.meta.hot) {
    // With hot module reloading, `import.meta.hot.data` is persisted.
    const root = (import.meta.hot.data.root ??= createRoot(element));
    root.render(app);
} else {
    // The hot module reloading API is not available in production.
    createRoot(element).render(app);
}
