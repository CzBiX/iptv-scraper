#!/usr/bin/ucode

'use strict';

let ubus = require("ubus").connect();
let status = ubus.call("network.interface.iptv", "status");

print('content-type: text/plain');
print('\n\n');
print(status['ipv4-address'][0]['address']);