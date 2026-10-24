import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import { Toaster } from "sonner";
import "./index.css";

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
    <Toaster theme="dark" position="top-right" toastOptions={{ style: { background: "hsl(240 10% 5.9%)", border: "1px solid hsl(240 3.7% 15.9%)" } }} />
  </React.StrictMode>
);
