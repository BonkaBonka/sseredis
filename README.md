# SSE Redis

This is a simple (extremely naïve) Server-Sent Event to Redis PUB/SUB bridge.  It's a mashup of code from [https://gist.github.com/artisonian/3836281](https://gist.github.com/artisonian/3836281) and [https://gist.github.com/tristanwietsma/5486625/](https://gist.github.com/tristanwietsma/5486625/) plus some small amount of my own legwork.

## BUGS!

  * It appears to leak Redis connections - if a connection is interrupted by, say, a proxy server, the HTTP connection is properly torn down but not the subscription channel.  Boo.
  * POSTs have no size limit (aside from any that Redis enforces).  I doubt this is a feature.
