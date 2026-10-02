import "@fontsource/inter/latin-400.css";
import "@fontsource/inter/latin-500.css";
import "@fontsource/inter/latin-600.css";
import "@fontsource/inter/latin-700.css";
import "@fontsource/inter/latin-800.css";
import "@fontsource/geist-mono/latin-400.css";
import { render } from "preact";
import { App } from "./App";
import "./styles.css";

render(<App />, document.getElementById("app")!);
