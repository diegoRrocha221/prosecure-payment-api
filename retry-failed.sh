#!/bin/bash

# Retry failed jobs script for ProSecure Payment API

# Load environment variables for Redis connection
set -a
source .env
set +a

# Extract Redis password from REDIS_URL
REDIS_PASSWORD=$(echo $REDIS_URL | sed -E 's/.*:\/\/(.*):(.*)@.*/\2/')
REDIS_HOST=$(echo $REDIS_URL | sed -E 's/.*@([^:]+).*/\1/')
REDIS_PORT=$(echo $REDIS_URL | sed -E 's/.*:([0-9]+).*/\1/')

# If no password in URL, set empty auth command
if [ -z "$REDIS_PASSWORD" ]; then
	    AUTH_CMD=""
    else
	        AUTH_CMD="AUTH $REDIS_PASSWORD"
fi

# Function to execute Redis command with authentication
redis_cmd() {
	    if [ -z "$AUTH_CMD" ]; then
		            redis-cli -h $REDIS_HOST -p $REDIS_PORT $@
			        else
					        redis-cli -h $REDIS_HOST -p $REDIS_PORT $AUTH_CMD $@
						    fi
					    }

				    # Get failed queue length
				    FAILED_COUNT=$(redis_cmd LLEN payment_jobs:failed)

				    if [ "$FAILED_COUNT" -eq 0 ]; then
					        echo "No failed jobs to retry."
						    exit 0
				    fi

				    echo "Found $FAILED_COUNT failed jobs. Moving them back to the main queue..."

				    # For each failed job, modify it and move to main queue
				    for i in $(seq 1 $FAILED_COUNT); do
					        # Get a job from the failed queue
						    JOB=$(redis_cmd LPOP payment_jobs:failed)
						        
						        if [ ! -z "$JOB" ]; then
								        # Reset retry count to 0
									        MODIFIED_JOB=$(echo $JOB | sed 's/"retry_count":[0-9]*/"retry_count":0/')
										        
										        # Push to main queue
											        redis_cmd RPUSH payment_jobs "$MODIFIED_JOB"
												        
												        echo "Moved job to main queue."
													    fi
												    done

												    echo "All failed jobs have been moved to the main queue for retry."
