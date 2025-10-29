import { useState, useEffect } from 'react'
import {GreetService, McpService} from "../bindings/github.com/VikashLoomba/mcp-client-manager-go/apps/mcp-manager-ui/index.js";
import {Events, WML} from "@wailsio/runtime";

function App() {
  const [name, setName] = useState<string>('');
  const [result, setResult] = useState<string>('Please enter your name below 👇');
  const [servers, setServers] = useState<string[]>([]);
  const [time, setTime] = useState<string>('Listening for Time event...');

  const doGreet = () => {
    let localName = name;
    if (!localName) {
      localName = 'anonymous';
    }
    GreetService.Greet(localName).then((resultValue: string) => {
      setResult(resultValue);
    }).catch((err: any) => {
      console.log(err);
    });
  }

  useEffect(() => {
    Events.On('time', (timeValue: any) => {
      setTime(timeValue.data);
    });
    // Reload WML so it picks up the wml tags
    WML.Reload();
  }, []);

  async function doListServers (): Promise<void> {
    const servers = await McpService.GetServers();
    console.log(servers);
    setServers(servers);
  }

  return (
    <div className="container">
      <div>
        <a data-wml-openURL="https://wails.io">
          <img src="/wails.png" className="logo" alt="Wails logo"/>
        </a>
        <a data-wml-openURL="https://reactjs.org">
          <img src="/react.svg" className="logo react" alt="React logo"/>
        </a>
      </div>
      <h1>Wails + React</h1>
      <div className="result">{result}</div>
      {servers.length > 0 && (
        <div className="servers-list">
          <h2>Servers:</h2>
          <ul>
            {servers.map((server, i) => (
              <li key={i}>
                {server}
              </li>
            ))
            }
          </ul>
          </div>
      )}
      <div className="card">
        <div className="input-box">
          <input className="input" value={name} onChange={(e) => setName(e.target.value)} type="text" autoComplete="off"/>
          <button className="btn" onClick={doGreet}>Greet</button>
          <button className="btn" onClick={doListServers}>Get Servers</button>
        </div>
      </div>
      <div className="footer">
        <div><p>Click on the Wails logo to learn more</p></div>
        <div><p>{time}</p></div>
      </div>
    </div>
  )
}

export default App
