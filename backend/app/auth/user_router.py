from fastapi import APIRouter, Depends, HTTPException
from sqlalchemy.orm import Session
from sqlalchemy.exc import IntegrityError
from ..database.connection import get_db
from ..database.schemas import UserCreate, UserResponse, UserLogin, Token
from .user_service import UserService
from .jwt_auth import verify_token

router = APIRouter()

@router.post("/register", response_model=UserResponse)
def register_user(user: UserCreate, db: Session = Depends(get_db)):
    try:
        new_user = UserService.create_user(db, user)
        return new_user
    except IntegrityError:
        db.rollback()
        raise HTTPException(status_code=400, detail="Email already registered")
    except Exception as e:
        db.rollback()
        raise HTTPException(status_code=400, detail=str(e))

@router.post("/login", response_model=Token)
def login(user_login: UserLogin, db: Session = Depends(get_db)):
    return UserService.login(db, user_login)

# Protected routes
@router.get("/me", response_model=UserResponse)
async def read_users_me(current_user: UserResponse = Depends(verify_token)):
    return current_user
