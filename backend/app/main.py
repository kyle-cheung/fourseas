import logging
from fastapi import FastAPI, Depends
from sqlalchemy.orm import Session
from .database.connection import get_db, engine, Base
from .auth.user_router import router as user_router
from .card.router import router as card_router

# Set up logging
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

logger.info("Starting application...")

# Create the database tables
try:
    Base.metadata.create_all(bind=engine)
    logger.info("Database tables created successfully.")
except Exception as e:
    logger.error(f"Error creating database tables: {str(e)}")
    raise


app = FastAPI()
app.include_router(user_router, prefix="/users", tags=["users"])
app.include_router(card_router, prefix="/api", tags=["credit cards"])

logger.info("FastAPI application instance created.")

@app.get("/")
async def root():
    return {"message": "Welcome to Fourseas Credit Card Command Center"}

# @app.get("/db-test")
# async def db_test(db: Session = Depends(get_db)):
#     try:
#         # Try to execute a simple query
#         db.execute("SELECT 1")
#         return {"message": "Database connection successful"}
#     except Exception as e:
#         return {"message": "Database connection failed", "error": str(e)}

logger.info("Application setup complete.")