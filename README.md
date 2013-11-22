# SSE Redis

This is a simple (extremely naïve) Server-Sent Event to Redis PUB/SUB bridge.  It's a mashup of code from [https://gist.github.com/artisonian/3836281](https://gist.github.com/artisonian/3836281) and [https://gist.github.com/tristanwietsma/5486625/](https://gist.github.com/tristanwietsma/5486625/) plus some small amount of my own legwork.

## Sample

	var es = EventSource('http://localhost:8080/redis/test');
	es.addEventListener('test', function (event) {
		console.log(event);
	}, false);

## BUGS!

  * It's lazy about closing Redis connections after an HTTP connection drop.  In fact, it won't bother trying to close the connection until some sort of output buffer in the HTTP package fills and causes a write.  Boo.
  * POSTs have no size limit (aside from any that Redis enforces).  I doubt this is a feature.
