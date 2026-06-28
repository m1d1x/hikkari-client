export namespace main {
	
	export class HealthInfo {
	    running: boolean;
	    tunnelOK: boolean;
	    ip: string;
	    status: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new HealthInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.running = source["running"];
	        this.tunnelOK = source["tunnelOK"];
	        this.ip = source["ip"];
	        this.status = source["status"];
	        this.message = source["message"];
	    }
	}
	export class ProfileInfo {
	    name: string;
	    label: string;
	    active: boolean;
	    endpoint: string;
	    protocol: string;
	
	    static createFrom(source: any = {}) {
	        return new ProfileInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.label = source["label"];
	        this.active = source["active"];
	        this.endpoint = source["endpoint"];
	        this.protocol = source["protocol"];
	    }
	}
	export class Whitelist {
	    domains: string[];
	    ips: string[];
	    processes: string[];
	
	    static createFrom(source: any = {}) {
	        return new Whitelist(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.domains = source["domains"];
	        this.ips = source["ips"];
	        this.processes = source["processes"];
	    }
	}

}

