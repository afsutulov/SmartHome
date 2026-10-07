import json, socket, struct, threading, subprocess, time, pathlib, tempfile, itertools

root = pathlib.Path(__file__).resolve().parent.parent
records, connections, subscriptions = [], [], []
lock = threading.Lock()
send_lock = threading.Lock()
message_ids = itertools.count(1)
server = socket.socket()
server.bind(('127.0.0.1', 0))
server.listen()

def exact(conn, n):
    result = b''
    while len(result) < n:
        part = conn.recv(n - len(result))
        if not part: raise EOFError()
        result += part
    return result

def packet(conn):
    header = exact(conn, 1)[0]
    length, scale = 0, 1
    while True:
        byte = exact(conn, 1)[0]
        length += (byte & 127) * scale
        if byte < 128: break
        scale *= 128
    return header, exact(conn, length)

def send(conn, header, payload):
    n, size = len(payload), b''
    while True:
        byte, n = n % 128, n // 128
        size += bytes([byte | (128 if n else 0)])
        if not n: break
    with send_lock:
        conn.sendall(bytes([header]) + size + payload)

def handle(conn, index):
    try:
        while True:
            header, body = packet(conn)
            kind = header >> 4
            if kind == 1: send(conn, 0x20, b'\0\0')
            elif kind == 8:
                pos, codes = 2, b''
                while pos < len(body):
                    n = int.from_bytes(body[pos:pos+2], 'big'); pos += 2
                    topic = body[pos:pos+n].decode(); pos += n
                    qos = body[pos]; pos += 1
                    with lock: subscriptions[index].append(topic)
                    codes += bytes([qos])
                send(conn, 0x90, body[:2] + codes)
            elif kind == 3:
                n = int.from_bytes(body[:2], 'big'); topic = body[2:2+n].decode()
                pos = n+2
                if ((header >> 1) & 3) == 1:
                    send(conn, 0x40, body[pos:pos+2]); pos += 2
                payload = body[pos:].decode()
                try: payload = json.loads(payload)
                except ValueError: pass
                with lock: records.append((time.monotonic(), topic, payload))
            elif kind == 12: send(conn, 0xd0, b'')
            elif kind == 14: return
    except (EOFError, OSError): pass

def accept():
    while True:
        try: conn, _ = server.accept()
        except OSError: return
        with lock:
            index = len(connections)
            connections.append(conn); subscriptions.append([])
        threading.Thread(target=handle, args=(conn,index), daemon=True).start()

def wait(predicate, timeout=12):
    deadline = time.monotonic()+timeout
    while time.monotonic()<deadline:
        if predicate(): return
        time.sleep(.02)
    raise AssertionError('timeout waiting for expected MQTT activity')

def inject_topic(topic, body, retained=False):
    topic = topic.encode()
    send(connections[-1],0x33 if retained else 0x32,struct.pack('!H',len(topic))+topic+struct.pack('!H',next(message_ids))+json.dumps(body).encode())

def inject(device, body, retained=False):
    inject_topic('zigbee2mqtt/'+device,body,retained)

def state_value(directory, device, field):
    path=pathlib.Path(directory)/'state.json'
    if not path.exists(): return None
    return json.loads(path.read_text())['devices'][device].get(field)


def alarms(value):
    with lock: return [r for r in records if isinstance(r[2],dict) and r[2].get('alarm') == value]

threading.Thread(target=accept,daemon=True).start()
with tempfile.TemporaryDirectory(dir=root) as directory:
    cfg=json.loads((root/'configs/smarthome-sutulov.conf').read_text())
    cfg['mqtt']['server']='tcp://127.0.0.1:'+str(server.getsockname()[1])
    cfg['storage']['state_file']=directory+'/state.json'
    cfg['logging']={'level':'warn','file':'','to_console':True}
    cfg['telegram']['token']='000:TEST_ONLY'
    cfg['telegram']['proxy']={'enabled':True,'type':'socks5','address':'127.0.0.1:1'}
    for device in cfg['devices']:
        if device['id']=='svet01': device.setdefault('initial',{})['state']=False
        if device['id']=='weather01': device.setdefault('initial',{})['last_seen']=77
    cfg['video_monitoring']['enabled']=False
    cfg['schedules']=[]
    path=pathlib.Path(directory)/'config.json';path.write_text(json.dumps(cfg))
    logfile=open(pathlib.Path(directory)/'service.log','w')
    process=subprocess.Popen([str(root/'bin/linux-amd64/SmartHome'),'--run','--config',str(path)],stdout=logfile,stderr=logfile)
    try:
        wait(lambda: subscriptions and len(subscriptions[0])>=30)
        inject_topic('/yandex/Voda01','true',retained=True)
        inject('0x00158d0006c58566',{'action':'single'},retained=True)
        inject('0x00158d0006c5fa47',{'temperature':26},retained=True)
        wait(lambda:state_value(directory,'weather01','temperature')==26)
        assert state_value(directory,'weather01','last_seen')==77, 'retained snapshot refreshed last_seen'
        assert not [r for r in records if r[1].endswith('/set')], 'retained command replayed'
        inject('0x00158d00073a6302',{'state_l1':'OFF'})
        inject('0x00158d00073a6302',{'state_l2':'OFF'})
        wait(lambda:state_value(directory,'svet01','state') is True)
        for value in range(50): inject('0x00158d0006c5fa47',{'temperature':value})
        wait(lambda:state_value(directory,'weather01','temperature')==49)
        # Defaults in a new/missing state file are not proof of dry sensors.
        inject_topic('/yandex/Voda01','true')
        inject('0x00158d0006c5fa47',{'temperature':49.5})
        wait(lambda:state_value(directory,'weather01','temperature')==49.5)
        assert not [r for r in records if r[1].endswith('/set')], 'unknown startup guard authorized opening'
        inject('0x00158d0006c50eb5',{'water_leak':True},retained=True)
        wait(lambda:len(alarms(1))==1)
        on=alarms(1)[0]
        assert on[2]=={'alarm':True,'melody':8,'volume':'medium'} and type(on[2]['alarm']) is bool
        kitchen=next(d for d in cfg['devices'] if d['id']=='voda_kitchen')
        inject_topic('/yandex/'+kitchen['yandex_id'],'true')
        inject('0x00158d0006c5fa47',{'temperature':50})
        wait(lambda:state_value(directory,'weather01','temperature')==50)
        assert not [r for r in records if r[1]=='zigbee2mqtt/'+kitchen['zigbee_id']+'/set' and isinstance(r[2],dict) and r[2].get('state_left')=='ON'], 'live voice reopened water during leak'
        # Никакой программный таймер не должен отправлять OFF через 30 секунд.
        time.sleep(30.4)
        assert not alarms(0), 'unexpected software auto-off'
        inject('0x00158d0006c58566',{'action':'double'})
        wait(lambda:len(alarms(0))==1)
        assert alarms(0)[0][2]=={'alarm':False} and type(alarms(0)[0][2]['alarm']) is bool
        with lock:
            connections[-1].shutdown(socket.SHUT_RDWR)
            connections[-1].close()
        wait(lambda:len(subscriptions)>=2 and len(subscriptions[-1])>=30)
        assert set(subscriptions[0])==set(subscriptions[1]), 'subscriptions were not restored'
        inject('0x00158d0006c50eb5',{'water_leak':True},retained=True)
        wait(lambda:len(alarms(1))==2)
        inject('0x00158d008b64810c',{'water_leak':True})
        wait(lambda:len(alarms(1))==3)
        result={'mqtt_protection_without_telegram':True,'boolean_on_off_payloads':True,'no_software_off_after_30_seconds':True,'double_click_off':True,'reconnect_resubscribed':True,'subscriptions_per_connection':len(subscriptions[0]),'retained_voice_and_button_ignored':True,'retained_leak_processed':True,'retained_last_seen_preserved':True,'partial_relay_reports':True,'ordered_report_burst':True,'live_open_blocked_during_leak':True,'unknown_startup_guard_blocked':True}
        print(json.dumps(result))
    finally:
        process.terminate();process.wait(timeout=5);logfile.close();server.close()
        assert process.returncode==0, 'SIGTERM did not exit cleanly'
